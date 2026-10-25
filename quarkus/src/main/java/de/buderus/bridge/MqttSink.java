package de.buderus.bridge;

import io.quarkus.runtime.annotations.RegisterForReflection;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicReference;
import org.eclipse.paho.client.mqttv3.IMqttActionListener;
import org.eclipse.paho.client.mqttv3.IMqttDeliveryToken;
import org.eclipse.paho.client.mqttv3.IMqttToken;
import org.eclipse.paho.client.mqttv3.MqttAsyncClient;
import org.eclipse.paho.client.mqttv3.MqttCallbackExtended;
import org.eclipse.paho.client.mqttv3.MqttConnectOptions;
import org.eclipse.paho.client.mqttv3.MqttException;
import org.eclipse.paho.client.mqttv3.MqttMessage;
import org.eclipse.paho.client.mqttv3.persist.MemoryPersistence;

@RegisterForReflection(classNames = {
        "org.eclipse.paho.client.mqttv3.internal.TCPNetworkModuleFactory",
        "org.eclipse.paho.client.mqttv3.internal.SSLNetworkModuleFactory",
        "org.eclipse.paho.client.mqttv3.internal.websocket.WebSocketNetworkModuleFactory",
        "org.eclipse.paho.client.mqttv3.internal.websocket.WebSocketSecureNetworkModuleFactory",
        "org.eclipse.paho.client.mqttv3.logging.JSR47Logger",
        "org.eclipse.paho.client.mqttv3.internal.nls.logcat"})
final class MqttSink implements AutoCloseable {
    private final Settings config;
    private final Events events;
    private final MqttAsyncClient client;
    private final AtomicReference<Object> dataPending = new AtomicReference<>();
    private final AtomicReference<Object> statusPending = new AtomicReference<>();
    private final AtomicBoolean closed = new AtomicBoolean();
    private final Object lifecycle = new Object();

    MqttSink(Settings config, Events events) throws MqttException {
        this.config = config;
        this.events = events;
        client = new MqttAsyncClient(normalizeBroker(config.broker()), config.clientId(), new MemoryPersistence());
        client.setCallback(new MqttCallbackExtended() {
            @Override public void connectComplete(boolean reconnect, String serverURI) {
                synchronized (lifecycle) {
                    if (!closed.get()) {
                        online();
                    }
                }
                events.log("info", reconnect ? "mqtt reconnected" : "mqtt connected");
            }
            @Override public void connectionLost(Throwable cause) {
                events.log("warn", "mqtt disconnected; reconnect pending");
            }
            @Override public void messageArrived(String topic, MqttMessage message) {}
            @Override public void deliveryComplete(IMqttDeliveryToken token) {}
        });
    }

    void connect() throws MqttException {
        MqttConnectOptions options = new MqttConnectOptions();
        options.setAutomaticReconnect(true);
        options.setCleanSession(false);
        options.setKeepAliveInterval(30);
        options.setConnectionTimeout((int) Math.max(1, Math.min(Integer.MAX_VALUE,
                config.mqttTimeout().toSeconds())));
        options.setMaxInflight(3); // One data PUBLISH, online replay and best-effort offline.
        options.setHttpsHostnameVerificationEnabled(true);
        if (!config.mqttUser().isEmpty()) {
            options.setUserName(config.mqttUser());
        }
        if (!config.mqttPassword().isEmpty()) {
            options.setPassword(config.mqttPassword().toCharArray());
        }
        if (config.availability()) {
            options.setWill(config.availabilityTopic(), bytes("offline"), Math.min(config.qos(), 1), true);
        }
        client.connect(options).waitForCompletion(timeoutMillis());
    }

    void publish(Mapping.Message message) throws MqttException, InterruptedException {
        if (message.topic().equals(config.availabilityTopic())) {
            throw new IllegalArgumentException("mapped topic conflicts with mqtt availability topic");
        }
        if (closed.get() || !client.isConnected()) {
            throw new IllegalStateException("mqtt is disconnected");
        }
        if (Thread.currentThread().isInterrupted()) {
            throw new InterruptedException();
        }
        Object marker = new Object();
        if (!dataPending.compareAndSet(null, marker)) {
            throw new IllegalStateException("mqtt publication is still pending");
        }
        IMqttDeliveryToken token;
        try {
            token = client.publish(message.topic(), message.payload(), config.qos(), message.retain(),
                    marker, completion(dataPending, marker));
        } catch (MqttException | RuntimeException e) {
            dataPending.compareAndSet(marker, null);
            throw e;
        }
        // A timed-out wait never opens the gate; only confirmed delivery does.
        await(token);
        dataPending.compareAndSet(marker, null);
    }

    private void online() {
        if (!config.availability()) {
            return;
        }
        Object marker = new Object();
        if (!statusPending.compareAndSet(null, marker)) {
            return;
        }
        try {
            client.publish(config.availabilityTopic(), bytes("online"), Math.min(config.qos(), 1), true,
                    marker, completion(statusPending, marker));
        } catch (MqttException e) {
            statusPending.compareAndSet(marker, null);
            events.log("warn", "mqtt online publication failed");
        }
    }

    private IMqttActionListener completion(AtomicReference<Object> pending, Object marker) {
        return new IMqttActionListener() {
            @Override public void onSuccess(IMqttToken token) {
                pending.compareAndSet(marker, null);
            }
            @Override public void onFailure(IMqttToken token, Throwable cause) {
                // A lost connection can fail waiting tokens while persistence still holds the PUBLISH.
                events.log("warn", "mqtt delivery remains pending");
            }
        };
    }

    private void await(IMqttToken token) throws MqttException, InterruptedException {
        long deadline = System.nanoTime() + config.mqttTimeout().toNanos();
        while (!token.isComplete()) {
            if (Thread.currentThread().isInterrupted()) {
                throw new InterruptedException();
            }
            long remaining = deadline - System.nanoTime();
            if (remaining <= 0) {
                throw new MqttException(MqttException.REASON_CODE_CLIENT_TIMEOUT);
            }
            try {
                token.waitForCompletion(Math.max(1, Math.min(100, remaining / 1_000_000)));
            } catch (MqttException e) {
                if (e.getReasonCode() != MqttException.REASON_CODE_CLIENT_TIMEOUT) {
                    throw e;
                }
            }
        }
        if (token.getException() != null) {
            throw token.getException();
        }
    }

    @Override
    public void close() {
        synchronized (lifecycle) {
            if (!closed.compareAndSet(false, true)) {
                return;
            }
            try {
                if (client.isConnected() && config.availability()) {
                    client.publish(config.availabilityTopic(), bytes("offline"), Math.min(config.qos(), 1), true)
                            .waitForCompletion(timeoutMillis());
                }
            } catch (MqttException e) {
                events.log("warn", "mqtt offline publication failed");
            } finally {
                try {
                    client.disconnectForcibly(0, timeoutMillis(), true);
                    client.close(true);
                } catch (MqttException e) {
                    events.log("warn", "mqtt close failed");
                }
            }
        }
    }

    private long timeoutMillis() {
        return Math.max(1, config.mqttTimeout().toMillis());
    }

    private static byte[] bytes(String value) {
        return value.getBytes(StandardCharsets.UTF_8);
    }

    static String normalizeBroker(String value) {
        URI uri = URI.create(value);
        String scheme = switch (uri.getScheme()) {
            case "mqtt" -> "tcp";
            case "tls", "mqtts" -> "ssl";
            default -> uri.getScheme();
        };
        int port = uri.getPort();
        if (port < 0) {
            port = switch (scheme) {
                case "ssl" -> 8883;
                case "ws" -> 80;
                case "wss" -> 443;
                default -> 1883;
            };
        }
        try {
            return new URI(scheme, null, uri.getHost(), port, uri.getPath(), uri.getQuery(), null).toString();
        } catch (java.net.URISyntaxException e) {
            throw new IllegalArgumentException("invalid MQTT_BROKER");
        }
    }
}
