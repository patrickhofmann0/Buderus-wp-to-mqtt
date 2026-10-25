package de.buderus.bridge;

import static org.junit.jupiter.api.Assertions.*;
import java.nio.charset.StandardCharsets;
import java.util.Map;
import java.util.concurrent.TimeUnit;
import java.util.function.BooleanSupplier;
import org.eclipse.paho.client.mqttv3.MqttException;
import org.junit.jupiter.api.Test;

class MqttSinkTest {
    private Settings settings(TestBroker broker, int qos) {
        var env = SettingsTest.env();
        env.putAll(Map.of("MQTT_BROKER", broker.uri(), "MQTT_QOS", Integer.toString(qos),
                "MQTT_PUBLISH_TIMEOUT", "2s"));
        return Settings.load(env);
    }

    private Mapping.Message message(String payload) {
        return new Mapping.Message("buderus/wp/temperature", payload.getBytes(StandardCharsets.UTF_8), true);
    }

    @Test
    void qosRetainAvailabilityAndGracefulShutdown() throws Exception {
        for (int qos = 0; qos <= 2; qos++) {
            try (TestBroker broker = new TestBroker()) {
                Settings settings = settings(broker, qos);
                try (MqttSink sink = new MqttSink(settings, new Events("error", "json"))) {
                    sink.connect();
                    await(() -> broker.messages.stream().anyMatch(p -> p.payload().equals("online")));
                    sink.publish(message("21"));
                    assertThrows(IllegalArgumentException.class, () -> sink.publish(
                            new Mapping.Message(settings.availabilityTopic(), new byte[0], false)));
                }
                await(() -> broker.messages.stream().anyMatch(p -> p.payload().equals("offline")));
                var online = broker.messages.stream().filter(p -> p.payload().equals("online")).findFirst().orElseThrow();
                var data = broker.messages.stream().filter(p -> p.payload().equals("21")).findFirst().orElseThrow();
                assertEquals(qos, data.qos());
                assertEquals(Math.min(qos, 1), online.qos());
                assertTrue(data.retained());
                assertTrue(online.retained());
            }
        }
    }

    @Test
    void timeoutKeepsGateClosedUntilRealAckAndReconnectReplays() throws Exception {
        try (TestBroker broker = new TestBroker()) {
            broker.acknowledge.set(false);
            try (MqttSink sink = new MqttSink(settings(broker, 1), new Events("error", "json"))) {
                sink.connect();
                assertThrows(MqttException.class, () -> sink.publish(message("pending")));
                assertThrows(IllegalStateException.class, () -> sink.publish(message("must not queue")));
                var pending = broker.messages.stream().filter(p -> p.payload().equals("pending")).findFirst().orElseThrow();
                pending.socket().close();
                await(() -> !broker.wills.isEmpty());
                assertEquals(1, broker.wills.getFirst().qos());
                broker.acknowledge.set(true);
                await(() -> broker.messages.stream().filter(p -> p.payload().equals("online")).count() >= 2);
                await(() -> broker.messages.stream().filter(p -> p.payload().equals("pending")).count() >= 2);
                // The retransmitted PUBLISH must be acknowledged before a subsequent data message can enter.
                Thread.sleep(100);
                sink.publish(message("after reconnect"));
                assertFalse(broker.messages.stream().anyMatch(p -> p.payload().equals("must not queue")));
            }
        }
    }

    @Test
    void aliasesNormalizeToReachableNativePahoTransports() {
        assertEquals("tcp://localhost:1883", MqttSink.normalizeBroker("mqtt://localhost"));
        assertEquals("ssl://localhost:8883", MqttSink.normalizeBroker("mqtts://localhost"));
        assertEquals("ssl://localhost:123", MqttSink.normalizeBroker("tls://localhost:123"));
        assertEquals("wss://localhost:443/mqtt", MqttSink.normalizeBroker("wss://localhost/mqtt"));
    }

    @Test
    void interruptedWaitDoesNotReleaseUnacknowledgedPublication() throws Exception {
        try (TestBroker broker = new TestBroker();
                MqttSink sink = new MqttSink(settings(broker, 1), new Events("error", "json"))) {
            broker.acknowledge.set(false);
            sink.connect();
            var failure = new java.util.concurrent.atomic.AtomicReference<Throwable>();
            Thread publisher = new Thread(() -> {
                try {
                    sink.publish(message("interrupted"));
                } catch (Throwable error) {
                    failure.set(error);
                }
            });
            publisher.start();
            await(() -> broker.messages.stream().anyMatch(p -> p.payload().equals("interrupted")));
            publisher.interrupt();
            publisher.join(2000);
            assertFalse(publisher.isAlive());
            assertInstanceOf(InterruptedException.class, failure.get());
            assertThrows(IllegalStateException.class, () -> sink.publish(message("must not queue")));
            broker.ack(broker.messages.stream().filter(p -> p.payload().equals("interrupted"))
                    .findFirst().orElseThrow());
            broker.acknowledge.set(true);
            Thread.sleep(100);
            sink.publish(message("after interrupted delivery"));
        }
    }

    @Test
    void closingDuringBrokerOutageStopsOwnedReconnectThread() throws Exception {
        try (TestBroker broker = new TestBroker();
                MqttSink sink = new MqttSink(settings(broker, 0), new Events("error", "json"))) {
            sink.connect();
            broker.close();
            await(() -> Thread.getAllStackTraces().keySet().stream()
                    .anyMatch(thread -> thread.isAlive() && thread.getName().equals("buderus-mqtt-reconnect")));
            sink.close();
            await(() -> Thread.getAllStackTraces().keySet().stream()
                    .noneMatch(thread -> thread.isAlive() && thread.getName().equals("buderus-mqtt-reconnect")));
            assertTrue(Thread.getAllStackTraces().keySet().stream()
                    .noneMatch(thread -> thread.isAlive() && thread.getName().startsWith("MQTT Reconnect:")));
        }
    }

    private static void await(BooleanSupplier condition) throws Exception {
        long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(8);
        while (!condition.getAsBoolean() && System.nanoTime() < deadline) {
            Thread.sleep(20);
        }
        assertTrue(condition.getAsBoolean(), "condition timed out");
    }
}
