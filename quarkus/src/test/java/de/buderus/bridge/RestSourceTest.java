package de.buderus.bridge;

import static org.junit.jupiter.api.Assertions.*;
import static de.buderus.bridge.SettingsTest.settings;
import com.sun.net.httpserver.HttpServer;
import com.sun.net.httpserver.HttpsServer;
import com.sun.net.httpserver.HttpsConfigurator;
import io.quarkus.test.junit.QuarkusTest;
import java.io.IOException;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.KeyStore;
import java.time.Duration;
import java.util.Map;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import javax.net.ssl.KeyManagerFactory;
import javax.net.ssl.SSLContext;
import org.junit.jupiter.api.Test;

@QuarkusTest
class RestSourceTest {
    @Test
    void closeIsIdempotentAndRejectsRequests() throws Exception {
        Settings settings = settings(SettingsTest.env());
        RestSource source = new RestSource(settings);
        source.close();
        source.close();
        IOException error = assertThrows(IOException.class,
                () -> source.fetch(settings.endpoints().getFirst()));
        assertEquals("http client is closed", error.getMessage());
    }

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
            Settings settings = settings(env);
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
        CountDownLatch disconnected = new CountDownLatch(1);
        server.createContext("/", exchange -> {
            exchange.sendResponseHeaders(200, 0);
            try {
                exchange.getResponseBody().write('x');
                exchange.getResponseBody().flush();
                started.countDown();
                for (int i = 0; i < 100; i++) {
                    Thread.sleep(20);
                    exchange.getResponseBody().write('x');
                    exchange.getResponseBody().flush();
                }
            } catch (IOException expected) {
                disconnected.countDown();
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
            } finally {
                exchange.close();
            }
        });
        server.start();
        var env = SettingsTest.env();
        env.put("API_URL", "http://127.0.0.1:" + server.getAddress().getPort());
        Settings settings = settings(env);
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
            assertTrue(disconnected.await(2, TimeUnit.SECONDS));
        } finally {
            server.stop(0);
        }
    }

    @Test
    void interruptBeforeHeadersClosesTheConnection() throws Exception {
        try (var server = new java.net.ServerSocket(0, 1, java.net.InetAddress.getByName("127.0.0.1"));
                var executor = Executors.newSingleThreadExecutor()) {
            CountDownLatch started = new CountDownLatch(1);
            var disconnected = executor.submit(() -> {
                try (var peer = server.accept()) {
                    peer.setSoTimeout(3000);
                    var input = peer.getInputStream();
                    int matched = 0;
                    byte[] end = "\r\n\r\n".getBytes(StandardCharsets.US_ASCII);
                    while (matched < end.length) {
                        int value = input.read();
                        if (value < 0) {
                            throw new IOException("request ended before headers");
                        }
                        matched = value == end[matched] ? matched + 1 : 0;
                    }
                    started.countDown();
                    return input.read();
                }
            });
            var env = SettingsTest.env();
            env.put("API_URL", "http://127.0.0.1:" + server.getLocalPort());
            Settings settings = settings(env);
            try (RestSource source = new RestSource(settings)) {
                var failure = new java.util.concurrent.atomic.AtomicReference<Throwable>();
                Thread worker = new Thread(() -> {
                    try {
                        source.fetch(settings.endpoints().getFirst());
                    } catch (Throwable error) {
                        failure.set(error);
                    }
                });
                worker.start();
                try {
                    assertTrue(started.await(2, TimeUnit.SECONDS));
                    worker.interrupt();
                    worker.join(2000);
                    assertFalse(worker.isAlive());
                    assertInstanceOf(InterruptedException.class, failure.get());
                    assertEquals(-1, disconnected.get(2, TimeUnit.SECONDS));
                } finally {
                    worker.interrupt();
                    worker.join(2000);
                }
            }
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
            Settings settings = settings(env);
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

    @Test
    void streamingIsBoundedAndPreservesOpaqueContentTypes() throws Exception {
        HttpServer server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        var executor = Executors.newCachedThreadPool();
        server.setExecutor(executor);
        byte[] text = "data: 9007199254740993\n\n{\"n\":1e+09}\n".getBytes(StandardCharsets.UTF_8);
        byte[] opaque = java.util.Arrays.copyOf(text, text.length + 2);
        opaque[opaque.length - 1] = (byte) 0xff;
        for (String type : new String[] {"application/json", "text/event-stream", "application/x-ndjson",
                "application/octet-stream"}) {
            server.createContext("/" + type.substring(type.indexOf('/') + 1), exchange -> {
                exchange.getResponseHeaders().set("Content-Type", type);
                exchange.sendResponseHeaders(200, 0);
                exchange.getResponseBody().write(opaque);
                exchange.close();
            });
        }
        CountDownLatch aborted = new CountDownLatch(1);
        server.createContext("/unbounded", exchange -> {
            exchange.sendResponseHeaders(200, 0);
            try {
                byte[] chunk = new byte[8192];
                for (int i = 0; i < 100000; i++) {
                    exchange.getResponseBody().write(chunk);
                    exchange.getResponseBody().flush();
                }
            } catch (IOException expected) {
                aborted.countDown();
            } finally {
                exchange.close();
            }
        });
        server.createContext("/drip", exchange -> {
            exchange.sendResponseHeaders(200, 0);
            try {
                for (int i = 0; i < 100; i++) {
                    exchange.getResponseBody().write('x');
                    exchange.getResponseBody().flush();
                    Thread.sleep(50);
                }
            } catch (IOException | InterruptedException expected) {
                // The client must abort despite continuously arriving body bytes.
            } finally {
                exchange.close();
            }
        });
        server.start();
        try {
            String base = "http://127.0.0.1:" + server.getAddress().getPort();
            var env = SettingsTest.env();
            env.putAll(Map.of("API_URL", base, "API_MAX_BODY_BYTES", "64", "API_TIMEOUT", "400ms"));
            try (RestSource source = new RestSource(settings(env))) {
                for (String path : new String[] {"/json", "/event-stream", "/x-ndjson", "/octet-stream"}) {
                    assertArrayEquals(opaque, source.fetch(new Settings.Endpoint("", java.net.URI.create(base + path))));
                }
                IOException large = assertThrows(IOException.class, () -> source.fetch(
                        new Settings.Endpoint("", java.net.URI.create(base + "/unbounded"))));
                assertEquals("response body exceeds limit", large.getMessage());
                assertTrue(aborted.await(2, TimeUnit.SECONDS));
                long start = System.nanoTime();
                assertThrows(IOException.class, () -> source.fetch(
                        new Settings.Endpoint("", java.net.URI.create(base + "/drip"))));
                assertTrue(Duration.ofNanos(System.nanoTime() - start).toMillis() < 1500);
            }
        } finally {
            server.stop(0);
            executor.shutdownNow();
        }
    }

    @Test
    void tlsVerificationIsLocalAndExplicit() throws Exception {
        Path keyStoreFile = Path.of("target", "rest-source-" + java.util.UUID.randomUUID() + ".p12");
        Files.createDirectories(keyStoreFile.getParent());
        char[] password = "test-password".toCharArray();
        Process keytool = new ProcessBuilder(Path.of(System.getProperty("java.home"), "bin", "keytool").toString(),
                "-genkeypair", "-alias", "test", "-keyalg", "RSA", "-storetype", "PKCS12",
                "-keystore", keyStoreFile.toString(), "-storepass", new String(password),
                "-dname", "CN=wrong-host.invalid", "-validity", "1", "-noprompt")
                .redirectErrorStream(true).start();
        try {
            assertTrue(keytool.waitFor(15, TimeUnit.SECONDS));
            assertEquals(0, keytool.exitValue());
            KeyStore keys = KeyStore.getInstance("PKCS12");
            try (var input = Files.newInputStream(keyStoreFile)) {
                keys.load(input, password);
            }
            KeyManagerFactory managers = KeyManagerFactory.getInstance(KeyManagerFactory.getDefaultAlgorithm());
            managers.init(keys, password);
            SSLContext tls = SSLContext.getInstance("TLS");
            tls.init(managers.getKeyManagers(), null, null);
            SSLContext originalDefault = SSLContext.getDefault();
            HttpsServer server = HttpsServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
            server.setHttpsConfigurator(new HttpsConfigurator(tls));
            server.createContext("/", exchange -> {
                exchange.sendResponseHeaders(200, 2);
                exchange.getResponseBody().write("{}".getBytes(StandardCharsets.UTF_8));
                exchange.close();
            });
            server.start();
            try {
                var env = SettingsTest.env();
                env.put("API_URL", "https://127.0.0.1:" + server.getAddress().getPort());
                Settings verified = settings(env);
                try (RestSource source = new RestSource(verified)) {
                    assertThrows(IOException.class, () -> source.fetch(verified.endpoints().getFirst()));
                }
                env.put("API_INSECURE_SKIP_VERIFY", "true");
                Settings insecure = settings(env);
                try (RestSource source = new RestSource(insecure)) {
                    assertArrayEquals("{}".getBytes(StandardCharsets.UTF_8),
                            source.fetch(insecure.endpoints().getFirst()));
                }
                try (RestSource source = new RestSource(verified)) {
                    assertThrows(IOException.class, () -> source.fetch(verified.endpoints().getFirst()));
                }
                assertSame(originalDefault, SSLContext.getDefault());
            } finally {
                server.stop(0);
            }
        } finally {
            keytool.destroyForcibly();
            Files.deleteIfExists(keyStoreFile);
        }

    }

}
