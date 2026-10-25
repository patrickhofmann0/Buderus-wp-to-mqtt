package de.buderus.bridge;

import static org.junit.jupiter.api.Assertions.*;
import com.sun.net.httpserver.HttpServer;
import java.io.IOException;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.Map;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import org.junit.jupiter.api.Test;

class RestSourceTest {
    @Test
    void boundedBodiesAuthStatusAndWholeBodyTimeout() throws Exception {
        HttpServer server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        var executor = Executors.newCachedThreadPool();
        server.setExecutor(executor);
        server.createContext("/ok", exchange -> {
            assertEquals("Bearer " + "placeholder", exchange.getRequestHeaders().getFirst("Authorization"));
            byte[] body = "1234".getBytes(StandardCharsets.UTF_8);
            exchange.sendResponseHeaders(200, body.length);
            exchange.getResponseBody().write(body);
            exchange.close();
        });
        server.createContext("/large", exchange -> {
            exchange.sendResponseHeaders(200, 5);
            exchange.getResponseBody().write("12345".getBytes(StandardCharsets.UTF_8));
            exchange.close();
        });
        server.createContext("/status", exchange -> {
            exchange.sendResponseHeaders(401, 6);
            exchange.getResponseBody().write("secret".getBytes(StandardCharsets.UTF_8));
            exchange.close();
        });
        CountDownLatch requestStarted = new CountDownLatch(1);
        server.createContext("/slow", exchange -> {
            exchange.sendResponseHeaders(200, 0);
            requestStarted.countDown();
            try {
                Thread.sleep(2000);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
            } finally {
                exchange.close();
            }
        });
        server.start();
        try {
            var env = SettingsTest.env();
            String base = "http://127.0.0.1:" + server.getAddress().getPort();
            env.putAll(Map.of("API_URL", base + "/ok", "API_TOKEN", "placeholder",
                    "API_MAX_BODY_BYTES", "4", "API_TIMEOUT", "250ms"));
            Settings settings = Settings.load(env);
            try (RestSource client = new RestSource(settings)) {
                assertArrayEquals("1234".getBytes(StandardCharsets.UTF_8), client.fetch(settings.endpoints().getFirst()));
                for (String path : new String[] {"/large", "/status", "/slow"}) {
                    var endpoint = new Settings.Endpoint("", java.net.URI.create(base + path));
                    long start = System.nanoTime();
                    IOException error = assertThrows(IOException.class, () -> client.fetch(endpoint));
                    assertFalse(error.getMessage().contains("secret"));
                    assertTrue(Duration.ofNanos(System.nanoTime() - start).toMillis() < 1800);
                }
                assertTrue(requestStarted.await(1, TimeUnit.SECONDS));
            }
        } finally {
            server.stop(0);
            executor.shutdownNow();
        }
    }

    @Test
    void interruptCancelsHttpWork() throws Exception {
        HttpServer server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        CountDownLatch started = new CountDownLatch(1);
        server.createContext("/", exchange -> {
            started.countDown();
            exchange.sendResponseHeaders(200, 0);
        });
        server.start();
        var env = SettingsTest.env();
        env.put("API_URL", "http://127.0.0.1:" + server.getAddress().getPort());
        Settings settings = Settings.load(env);
        try (RestSource client = new RestSource(settings)) {
            var failure = new java.util.concurrent.atomic.AtomicReference<Throwable>();
            Thread worker = new Thread(() -> {
                try {
                    client.fetch(settings.endpoints().getFirst());
                } catch (Throwable error) {
                    failure.set(error);
                }
            });
            worker.start();
            assertTrue(started.await(2, TimeUnit.SECONDS));
            worker.interrupt();
            worker.join(2000);
            assertFalse(worker.isAlive());
            assertInstanceOf(InterruptedException.class, failure.get());
        } finally {
            server.stop(0);
        }
    }

    @Test
    void basicAuthenticationAndRedirectsAreExplicit() throws Exception {
        HttpServer server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        server.createContext("/basic", exchange -> {
            String expected = "Basic " + java.util.Base64.getEncoder().encodeToString(
                    "example-user:example-password".getBytes(StandardCharsets.UTF_8));
            int status = expected.equals(exchange.getRequestHeaders().getFirst("Authorization")) ? 200 : 401;
            exchange.sendResponseHeaders(status, 2);
            exchange.getResponseBody().write("{}".getBytes(StandardCharsets.UTF_8));
            exchange.close();
        });
        server.createContext("/redirect", exchange -> {
            exchange.getResponseHeaders().add("Location", "/basic");
            exchange.sendResponseHeaders(302, -1);
            exchange.close();
        });
        server.start();
        try {
            var env = SettingsTest.env();
            String base = "http://127.0.0.1:" + server.getAddress().getPort();
            env.putAll(Map.of("API_URL", base + "/basic", "API_USER", "example-user",
                    "API_PASSWORD", "example-password"));
            Settings settings = Settings.load(env);
            try (RestSource source = new RestSource(settings)) {
                assertArrayEquals("{}".getBytes(StandardCharsets.UTF_8),
                        source.fetch(settings.endpoints().getFirst()));
                IOException exception = assertThrows(IOException.class, () -> source.fetch(
                        new Settings.Endpoint("", java.net.URI.create(base + "/redirect"))));
                assertEquals("http status 302", exception.getMessage());
            }
        } finally {
            server.stop(0);
        }
    }
}
