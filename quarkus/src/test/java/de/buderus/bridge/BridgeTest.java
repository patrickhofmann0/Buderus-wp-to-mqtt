package de.buderus.bridge;

import static org.junit.jupiter.api.Assertions.*;
import com.sun.net.httpserver.HttpServer;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.Map;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.jupiter.api.Test;

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
        try (TestBroker broker = new TestBroker(); Bridge bridge = new Bridge()) {
            var env = SettingsTest.env();
            env.putAll(Map.of("API_URL", "http://127.0.0.1:" + api.getAddress().getPort(),
                    "MQTT_BROKER", broker.uri(), "MQTT_PUBLISH_TIMEOUT", "2s",
                    "SCHEDULE", "@every 1s", "LOG_LEVEL", "error"));
            bridge.start(Settings.load(env));
            assertTrue(started.await(3, TimeUnit.SECONDS));
            for (int i = 0; i < 100; i++) {
                bridge.trigger();
            }
            Thread.sleep(1200);
            assertEquals(1, requests.get());
            long start = System.nanoTime();
            bridge.close();
            assertTrue(TimeUnit.NANOSECONDS.toSeconds(System.nanoTime() - start) < 8);
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
        try (TestBroker broker = new TestBroker(); Bridge bridge = new Bridge()) {
            var env = SettingsTest.env();
            env.putAll(Map.of("API_URL", "http://127.0.0.1:" + api.getAddress().getPort(),
                    "MQTT_BROKER", broker.uri(), "LOG_LEVEL", "error"));
            bridge.start(Settings.load(env));
            assertTrue(fetched.await(3, TimeUnit.SECONDS));
            Thread.sleep(200);
            bridge.close();
            assertFalse(broker.messages.stream().anyMatch(p -> p.topic().equals("buderus/wp/a")));
            assertFalse(broker.messages.stream().anyMatch(p -> p.payload().equals("not online")));
        } finally {
            api.stop(0);
        }
    }
}
