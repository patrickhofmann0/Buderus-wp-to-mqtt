package de.buderus.bridge;

import io.quarkus.runtime.annotations.RegisterForReflection;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.ScheduledFuture;
import java.util.concurrent.ScheduledThreadPoolExecutor;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;
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
        "org.eclipse.paho.client.mqttv3.logging.JSR47Logger"})
final class MqttSink implements AutoCloseable {
    private final Settings config;
    private final Events events;
    private final MqttAsyncClient client;
    private final AtomicReference<Pending> dataPending = new AtomicReference<>();
    private final AtomicReference<Pending> statusPending = new AtomicReference<>();
    private final AtomicBoolean closed = new AtomicBoolean();
    private final Object lifecycle = new Object();
    private final ScheduledThreadPoolExecutor reconnect = new ScheduledThreadPoolExecutor(1,
            task -> new Thread(task, "buderus-mqtt-reconnect"));
    private ScheduledFuture<?> reconnectTask;
    private boolean reconnectPending;
    private boolean connectedBefore;
    private long reconnectDelayMillis = 1000;

    MqttSink(Settings config, Events events) throws MqttException {
        this(config, events, new MqttAsyncClient(normalizeBroker(config.broker()), config.clientId(),
                new MemoryPersistence()));
    }

    MqttSink(Settings config, Events events, MqttAsyncClient client) {
        this.config = config;
        this.events = events;
        this.client = client;
        reconnect.setRemoveOnCancelPolicy(true);
        client.setCallback(new MqttCallbackExtended() {
            @Override public void connectComplete(boolean reconnect, String serverURI) {
                synchronized (lifecycle) {
                    reconnectPending = false;
                    if (reconnectTask != null) {
                        reconnectTask.cancel(false);
                    }
                    if (closed.get()) {
                        return;
                    }
                    if (!client.isConnected()) {
                        scheduleReconnect();
                        return;
                    }
                    reconnectDelayMillis = 1000;
                    events.log("info", connectedBefore ? "mqtt reconnected" : "mqtt connected");
                    connectedBefore = true;
                    online();
                }
            }
            @Override public void connectionLost(Throwable cause) {
                synchronized (lifecycle) {
                    // Paho discards ordinary QoS0 messages before this callback; no ACK will follow.
                    if (config.qos() == 0) {
                        discard(dataPending);
                        discard(statusPending);
                    }
                    scheduleReconnect();
                }
                events.log("warn", "mqtt disconnected; reconnect pending");
            }
            @Override public void messageArrived(String topic, MqttMessage message) {}
            @Override public void deliveryComplete(IMqttDeliveryToken token) {}
        });
    }

    void connect() throws MqttException {
        client.connect(connectionOptions()).waitForCompletion(timeoutMillis());
    }

    private MqttConnectOptions connectionOptions() {
        MqttConnectOptions options = new MqttConnectOptions();
        // Own reconnect scheduling: Paho's automatic reconnect timer can outlive close during an outage.
        options.setAutomaticReconnect(false);
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
        return options;
    }

    private void scheduleReconnect() {
        if (closed.get() || reconnectPending) {
            return;
        }
        reconnectPending = true;
        long delay = reconnectDelayMillis;
        reconnectDelayMillis = Math.min(120_000, reconnectDelayMillis * 2);
        reconnectTask = reconnect.schedule(this::reconnect, delay, TimeUnit.MILLISECONDS);
    }

    private void reconnect() {
        synchronized (lifecycle) {
            if (closed.get()) {
                reconnectPending = false;
                return;
            }
            try {
                client.connect(connectionOptions(), null, new IMqttActionListener() {
                    @Override public void onSuccess(IMqttToken token) {
                        synchronized (lifecycle) {
                            reconnectPending = false;
                        }
                    }
                    @Override public void onFailure(IMqttToken token, Throwable cause) {
                        synchronized (lifecycle) {
                            reconnectPending = false;
                            scheduleReconnect();
                        }
                    }
                });
            } catch (MqttException | RuntimeException e) {
                reconnectPending = false;
                scheduleReconnect();
            }
        }
    }

    void publish(Mapping.Message message) throws MqttException, InterruptedException {
        if (message.topic().equals(config.availabilityTopic())) {
            throw new IllegalArgumentException("mapped topic conflicts with mqtt availability topic");
        }
        if (Thread.currentThread().isInterrupted()) {
            throw new InterruptedException();
        }
        Pending marker = new Pending();
        synchronized (lifecycle) {
            if (closed.get() || !client.isConnected()) {
                throw new IllegalStateException("mqtt is disconnected");
            }
            if (!dataPending.compareAndSet(null, marker)) {
                throw new IllegalStateException("mqtt publication is still pending");
            }
            try {
                client.publish(message.topic(), message.payload(), config.qos(), message.retain(),
                        marker, completion(dataPending, marker));
            } catch (MqttException | RuntimeException e) {
                dataPending.compareAndSet(marker, null);
                throw e;
            }
        }
        // Do not wait on Paho's token: an interrupted token wait mutates its delivery error state.
        try {
            marker.delivered.get(config.mqttTimeout().toNanos(), TimeUnit.NANOSECONDS);
        } catch (TimeoutException e) {
            throw new MqttException(MqttException.REASON_CODE_CLIENT_TIMEOUT);
        } catch (ExecutionException e) {
            throw new MqttException(e.getCause());
        }
    }

    private void online() {
        if (!config.availability()) {
            return;
        }
        Pending marker = new Pending();
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

    private static final class Pending {
        private final CompletableFuture<Void> delivered = new CompletableFuture<>();
    }

    private static void discard(AtomicReference<Pending> pending) {
        Pending marker = pending.getAndSet(null);
        if (marker != null) {
            marker.delivered.completeExceptionally(new MqttException(MqttException.REASON_CODE_CLIENT_NOT_CONNECTED));
        }
    }

    private IMqttActionListener completion(AtomicReference<Pending> pending, Pending marker) {
        return new IMqttActionListener() {
            @Override public void onSuccess(IMqttToken token) {
                pending.compareAndSet(marker, null);
                marker.delivered.complete(null);
            }
            @Override public void onFailure(IMqttToken token, Throwable cause) {
                // A lost connection can fail waiting tokens while persistence still holds the PUBLISH.
                events.log("warn", "mqtt delivery remains pending");
                marker.delivered.completeExceptionally(cause);
            }
        };
    }

    @Override
    public void close() {
        IMqttToken offline = null;
        synchronized (lifecycle) {
            if (!closed.compareAndSet(false, true)) {
                return;
            }
            reconnect.shutdownNow();
            try {
                if (client.isConnected() && config.availability()) {
                    offline = client.publish(config.availabilityTopic(), bytes("offline"), Math.min(config.qos(), 1), true);
                }
            } catch (MqttException e) {
                events.log("warn", "mqtt offline publication failed");
            }
        }
        try {
            if (offline != null) {
                offline.waitForCompletion(timeoutMillis());
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
