package de.buderus.bridge;

import io.quarkus.arc.Arc;
import io.quarkus.rest.client.reactive.QuarkusRestClientBuilder;
import io.quarkus.tls.TlsConfigurationRegistry;
import jakarta.annotation.PreDestroy;
import jakarta.inject.Inject;
import jakarta.inject.Singleton;
import java.io.IOException;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.util.Base64;
import java.util.Set;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;
import java.util.concurrent.atomic.AtomicBoolean;

@Singleton
final class RestSource implements AutoCloseable {
    private final HeatPumpApi client;
    private final Settings config;
    private final String authorization;
    private final Set<CompletableFuture<?>> requests = ConcurrentHashMap.newKeySet();
    private final AtomicBoolean closed = new AtomicBoolean();

    @Inject
    RestSource(Settings config) {
        this.config = config;
        authorization = !config.apiToken().isEmpty() ? "Bearer " + config.apiToken()
                : !config.apiUser().isEmpty() ? "Basic " + Base64.getEncoder().encodeToString(
                        (config.apiUser() + ":" + config.apiPassword()).getBytes(StandardCharsets.UTF_8))
                : null;
        var builder = QuarkusRestClientBuilder.newBuilder()
                .baseUri(config.endpoints().getFirst().uri())
                .connectTimeout(config.apiTimeout().toNanos(), TimeUnit.NANOSECONDS)
                .readTimeout(config.apiTimeout().toNanos(), TimeUnit.NANOSECONDS)
                .followRedirects(false)
                .disableDefaultMapper(true)
                .register(new BoundedResponseFilter(config.maxBody()));
        if (config.insecure()) {
            var registry = Arc.container().instance(TlsConfigurationRegistry.class).get();
            builder.tlsConfiguration(registry.get("buderus-api-insecure").orElseThrow(
                    () -> new IllegalStateException("missing API TLS configuration")));
        }
        client = builder.build(HeatPumpApi.class);
    }

    byte[] fetch(Settings.Endpoint endpoint) throws IOException, InterruptedException {
        if (closed.get()) {
            throw new IOException("http client is closed");
        }
        CompletableFuture<InputStream> response = null;
        try {
            response = client.fetch(endpoint.uri(), authorization).subscribeAsCompletionStage();
            requests.add(response);
            try (var body = response.get(config.apiTimeout().toNanos(), TimeUnit.NANOSECONDS)) {
                byte[] bytes = body.readNBytes(config.maxBody() + 1);
                if (bytes.length > config.maxBody()) {
                    throw new IOException("response body exceeds limit");
                }
                return bytes;
            }
        } catch (TimeoutException e) {
            throw new IOException("http request timed out");
        } catch (ExecutionException e) {
            for (Throwable cause = e.getCause(); cause != null; cause = cause.getCause()) {
                if (cause instanceof BoundedResponseFilter.StatusException status) {
                    throw new IOException("http status " + status.status);
                }
                if (cause instanceof BoundedResponseFilter.BodyLimitException) {
                    throw new IOException("response body exceeds limit");
                }
            }
            throw new IOException("http request failed");
        } catch (RuntimeException e) {
            throw new IOException("http request failed");
        } finally {
            if (response != null) {
                requests.remove(response);
                response.cancel(true);
            }
        }
    }

    @Override
    @PreDestroy
    public void close() {
        if (!closed.compareAndSet(false, true)) {
            return;
        }
        requests.forEach(request -> request.cancel(true));
        try {
            client.close();
        } catch (IOException e) {
            throw new IllegalStateException("http client close failed");
        }
    }
}
