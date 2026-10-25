package de.buderus.bridge;

import static org.junit.jupiter.api.Assertions.*;
import com.sun.net.httpserver.HttpServer;
import io.quarkus.test.junit.QuarkusTest;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.Map;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.jupiter.api.Test;

@QuarkusTest
class BridgeTest {
    @Test
    void startupAndTimerShareExclusionAndCloseInterruptsWork() throws Exception {
        HttpServer api = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        var executor = Executors.newCachedThreadPool();
        api.setExecutor(executor);
        CountDownLatch started = new CountDownLatch(1);
        CountDownLatch finish = new CountDownLatch(1);
        AtomicInteger requests = new AtomicInteger();
        api.createContext("/", exchange -> {
            requests.incrementAndGet();
            started.countDown();
            try {
                finish.await(15, TimeUnit.SECONDS);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
            } finally {
                exchange.close();
            }
        });
        api.start();
        try (TestBroker broker = new TestBroker()) {
            var env = SettingsTest.env();
            env.putAll(Map.of("bridge-sensitive.api-url", "http://127.0.0.1:" + api.getAddress().getPort(),
                    "bridge.mqtt-broker", broker.uri(), "bridge.mqtt-publish-timeout", "2s",
                    "bridge.schedule", "@every 1s", "bridge.log-level", "error"));
            try (Bridge bridge = bridge(SettingsTest.settings(env))) {
                bridge.start();
                assertTrue(started.await(3, TimeUnit.SECONDS));
                for (int i = 0; i < 100; i++) {
                    bridge.trigger();
                }
                Thread.sleep(1200);
                assertEquals(1, requests.get());
                long start = System.nanoTime();
                bridge.close();
                assertTrue(TimeUnit.NANOSECONDS.toSeconds(System.nanoTime() - start) < 8);
            }
        } finally {
            finish.countDown();
            api.stop(0);
            executor.shutdownNow();
        }
    }

    @Test
    void availabilityCollisionRejectsWholeMappingBeforePublishing() throws Exception {
        HttpServer api = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        CountDownLatch fetched = new CountDownLatch(1);
        api.createContext("/", exchange -> {
            byte[] body = "{\"a\":1,\"status\":\"not online\"}".getBytes(StandardCharsets.UTF_8);
            exchange.sendResponseHeaders(200, body.length);
            exchange.getResponseBody().write(body);
            exchange.close();
            fetched.countDown();
        });
        api.start();
        try (TestBroker broker = new TestBroker()) {
            var env = SettingsTest.env();
            env.putAll(Map.of("bridge-sensitive.api-url", "http://127.0.0.1:" + api.getAddress().getPort(),
                    "bridge.mqtt-broker", broker.uri(), "bridge.log-level", "error"));
            try (Bridge bridge = bridge(SettingsTest.settings(env))) {
                bridge.start();
                assertTrue(fetched.await(3, TimeUnit.SECONDS));
                Thread.sleep(200);
                bridge.close();
                assertFalse(broker.messages.stream().anyMatch(p -> p.topic().equals("buderus/wp/a")));
                assertFalse(broker.messages.stream().anyMatch(p -> p.payload().equals("not online")));
            }
        } finally {
            api.stop(0);
        }
    }

    private static Bridge bridge(Settings settings) throws Exception {
        return new Bridge(settings, new RestSource(settings), new MqttSink(settings));
    }
}
