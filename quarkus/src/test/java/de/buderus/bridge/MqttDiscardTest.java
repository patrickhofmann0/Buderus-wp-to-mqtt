package de.buderus.bridge;

import static org.junit.jupiter.api.Assertions.*;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import org.eclipse.paho.client.mqttv3.IMqttActionListener;
import org.eclipse.paho.client.mqttv3.IMqttDeliveryToken;
import org.eclipse.paho.client.mqttv3.IMqttToken;
import org.eclipse.paho.client.mqttv3.MqttAsyncClient;
import org.eclipse.paho.client.mqttv3.MqttCallback;
import org.eclipse.paho.client.mqttv3.MqttCallbackExtended;
import org.eclipse.paho.client.mqttv3.MqttConnectOptions;
import org.eclipse.paho.client.mqttv3.MqttException;
import org.eclipse.paho.client.mqttv3.persist.MemoryPersistence;
import org.junit.jupiter.api.Test;

class MqttDiscardTest {
    @Test
    void discardedQosZeroDataAndOnlineDoNotPermanentlyBlockReconnect() throws Exception {
        try (ControlledClient client = new ControlledClient()) {
            Settings settings = settings(0);
            try (MqttSink sink = new MqttSink(settings, new Events("error", "json"), client)) {
                client.reconnect(false);
                assertThrows(MqttException.class, () -> sink.publish(message()));
                assertThrows(IllegalStateException.class, () -> sink.publish(message()));
                client.drop();
                client.reconnect(true);
                assertEquals(2, client.messages.stream().filter(p -> p.topic().endsWith("/status")).count());
                client.completeImmediately = true;
                sink.publish(message());
                sink.publish(message());
                assertEquals(5, client.messages.size());
                client.connected = false;
            }
        }
    }

    @Test
    void persistedQosOneAndTwoRemainPendingAcrossDisconnectUntilAck() throws Exception {
        for (int qos : new int[] {1, 2}) {
            try (ControlledClient client = new ControlledClient()) {
                try (MqttSink sink = new MqttSink(settings(qos), new Events("error", "json"), client)) {
                    client.reconnect(false);
                    assertThrows(MqttException.class, () -> sink.publish(message()));
                    client.drop();
                    client.reconnect(true);
                    assertEquals(1, client.messages.stream().filter(p -> p.topic().endsWith("/status")).count());
                    assertThrows(IllegalStateException.class, () -> sink.publish(message()));
                    client.messages.get(0).listener().onSuccess(null);
                    client.messages.get(1).listener().onSuccess(null);
                    client.completeImmediately = true;
                    sink.publish(message());
                    assertEquals(3, client.messages.size());
                    client.connected = false;
                }
            }
        }
    }

    private static Settings settings(int qos) {
        var env = SettingsTest.env();
        env.putAll(Map.of("MQTT_QOS", Integer.toString(qos), "MQTT_PUBLISH_TIMEOUT", "20ms"));
        return Settings.load(env);
    }

    private static Mapping.Message message() {
        return new Mapping.Message("buderus/wp/temperature", new byte[] {1}, false);
    }

    private static final class ControlledClient extends MqttAsyncClient {
        private record Publication(String topic, IMqttActionListener listener) {}
        private final List<Publication> messages = new ArrayList<>();
        private MqttCallbackExtended callback;
        private boolean connected = true;
        private boolean completeImmediately;

        ControlledClient() throws MqttException {
            super("tcp://localhost:1883", "controlled", new MemoryPersistence());
        }

        @Override public void setCallback(MqttCallback callback) {
            this.callback = (MqttCallbackExtended) callback;
        }

        @Override public boolean isConnected() {
            return connected;
        }

        @Override
        public IMqttToken connect(MqttConnectOptions options, Object context, IMqttActionListener listener)
                throws MqttException {
            throw new MqttException(MqttException.REASON_CODE_CLIENT_NOT_CONNECTED);
        }

        @Override
        public IMqttDeliveryToken publish(String topic, byte[] payload, int qos, boolean retain,
                Object context, IMqttActionListener listener) {
            messages.add(new Publication(topic, listener));
            if (completeImmediately) {
                listener.onSuccess(null);
            }
            return null;
        }

        void drop() {
            connected = false;
            callback.connectionLost(new MqttException(MqttException.REASON_CODE_CONNECTION_LOST));
        }

        void reconnect(boolean reconnect) {
            connected = true;
            callback.connectComplete(reconnect, "tcp://localhost:1883");
        }

        @Override public void disconnectForcibly(long quiesceTimeout, long disconnectTimeout, boolean sendDisconnect) {
            connected = false;
        }
    }
}
