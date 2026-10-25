package de.buderus.bridge;

import static org.junit.jupiter.api.Assertions.*;

import com.sun.net.httpserver.HttpServer;
import io.quarkus.test.common.QuarkusTestResource;
import io.quarkus.test.common.QuarkusTestResourceLifecycleManager;
import io.quarkus.test.junit.QuarkusTest;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.Map;
import java.util.concurrent.TimeUnit;
import java.util.function.BooleanSupplier;
import org.junit.jupiter.api.Test;

@QuarkusTest
@QuarkusTestResource(value = TransportLifecycleTest.Transports.class, restrictToAnnotatedClass = true)
class TransportLifecycleTest {
    @Inject
    Instance<Bridge> bridges;

    @Inject
    Instance<MqttSink> sinks;

    TestBroker broker;

    @Test
    void cdiOwnsLazyTransportsAndClosesThemWithTheBridge() throws Exception {
        Bridge bridge = bridges.get();
        MqttSink sink = sinks.get();
        assertSame(sink, sinks.get());
        assertTrue(broker.sockets.isEmpty(), "CDI construction must not connect to the broker");
        try {
            bridge.start();
            await(() -> broker.messages.stream().anyMatch(p -> p.payload().equals("online")));
            bridge.trigger();
            await(() -> broker.messages.stream().anyMatch(p ->
                    p.topic().equals("buderus/wp/temperature") && p.payload().equals("21.5")));
        } finally {
            bridges.destroy(bridge);
        }
        await(() -> broker.messages.stream().anyMatch(p -> p.payload().equals("offline")));
        assertThrows(IllegalStateException.class, () ->
                sink.publish(new Mapping.Message("buderus/wp/temperature", new byte[] {1}, false)));
        sinks.destroy(sink);
    }

    private static void await(BooleanSupplier condition) throws InterruptedException {
        long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5);
        while (!condition.getAsBoolean() && System.nanoTime() < deadline) {
            Thread.sleep(10);
        }
        assertTrue(condition.getAsBoolean(), "transport operation did not complete");
    }

    public static class Transports implements QuarkusTestResourceLifecycleManager {
        private TestBroker broker;
        private HttpServer api;

        @Override
        public Map<String, String> start() {
            try {
                broker = new TestBroker();
                api = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
                api.createContext("/", exchange -> {
                    byte[] body = "{\"temperature\":21.5}".getBytes(StandardCharsets.UTF_8);
                    exchange.sendResponseHeaders(200, body.length);
                    exchange.getResponseBody().write(body);
                    exchange.close();
                });
                api.start();
                return Map.of("API_URL", "http://127.0.0.1:" + api.getAddress().getPort(),
                        "API_URLS", "", "MQTT_BROKER", broker.uri(), "MQTT_QOS", "1",
                        "MQTT_PUBLISH_TIMEOUT", "1s", "POLL_ON_START", "false",
                        "SCHEDULE", "@every 1h", "LOG_LEVEL", "error");
            } catch (Exception e) {
                stop();
                throw new IllegalStateException("cannot start local transports", e);
            }
        }

        @Override
        public void inject(TestInjector injector) {
            injector.injectIntoFields(broker, new TestInjector.MatchesType(TestBroker.class));
        }

        @Override
        public void stop() {
            if (api != null) {
                api.stop(0);
            }
            if (broker != null) {
                try {
                    broker.close();
                } catch (Exception e) {
                    throw new IllegalStateException("cannot stop local broker", e);
                }
            }
        }
    }
}
