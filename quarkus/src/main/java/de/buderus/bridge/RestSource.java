package de.buderus.bridge;

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.net.Socket;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.ByteBuffer;
import java.nio.charset.StandardCharsets;
import java.security.GeneralSecurityException;
import java.security.cert.X509Certificate;
import java.time.Duration;
import java.util.Base64;
import java.util.List;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CompletionStage;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.Flow;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;
import javax.net.ssl.SSLContext;
import javax.net.ssl.SSLEngine;
import javax.net.ssl.SSLParameters;
import javax.net.ssl.X509ExtendedTrustManager;

final class RestSource implements AutoCloseable {
    private final HttpClient client;
    private final Settings config;

    RestSource(Settings config) throws GeneralSecurityException {
        this.config = config;
        HttpClient.Builder builder = HttpClient.newBuilder().connectTimeout(config.apiTimeout())
                .followRedirects(HttpClient.Redirect.NEVER);
        if (config.insecure()) {
            SSLContext context = SSLContext.getInstance("TLS");
            // Extended trust-manager callbacks deliberately bypass both certificate and peer identity checks.
            context.init(null, new X509ExtendedTrustManager[] {new InsecureTrust()}, null);
            SSLParameters parameters = new SSLParameters();
            parameters.setEndpointIdentificationAlgorithm("");
            builder.sslContext(context).sslParameters(parameters);
        }
        client = builder.build();
    }

    byte[] fetch(Settings.Endpoint endpoint) throws IOException, InterruptedException {
        HttpRequest.Builder request = HttpRequest.newBuilder(endpoint.uri()).timeout(config.apiTimeout()).GET();
        if (!config.apiToken().isEmpty()) {
            request.header("Authorization", "Bearer " + config.apiToken());
        } else if (!config.apiUser().isEmpty()) {
            String credentials = config.apiUser() + ":" + config.apiPassword();
            request.header("Authorization", "Basic " + Base64.getEncoder()
                    .encodeToString(credentials.getBytes(StandardCharsets.UTF_8)));
        }
        CompletableFuture<HttpResponse<byte[]>> response = client.sendAsync(request.build(),
                info -> new LimitedBody(config.maxBody()));
        try {
            HttpResponse<byte[]> result = response.get(config.apiTimeout().toNanos(), TimeUnit.NANOSECONDS);
            if (result.statusCode() < 200 || result.statusCode() >= 300) {
                throw new IOException("http status " + result.statusCode());
            }
            return result.body();
        } catch (TimeoutException e) {
            throw new IOException("http request timed out");
        } catch (ExecutionException e) {
            throw new IOException("http request failed");
        } finally {
            // Cancellation terminates network reads as well as the caller's wait.
            response.cancel(true);
        }
    }

    @Override
    public void close() {
        client.shutdownNow();
    }

    private static final class LimitedBody implements HttpResponse.BodySubscriber<byte[]> {
        private final CompletableFuture<byte[]> body = new CompletableFuture<>();
        private final ByteArrayOutputStream bytes = new ByteArrayOutputStream();
        private final int limit;
        private Flow.Subscription subscription;

        LimitedBody(int limit) {
            this.limit = limit;
        }

        @Override
        public CompletionStage<byte[]> getBody() {
            return body;
        }

        @Override
        public void onSubscribe(Flow.Subscription subscription) {
            this.subscription = subscription;
            subscription.request(1);
        }

        @Override
        public void onNext(List<ByteBuffer> chunks) {
            for (ByteBuffer chunk : chunks) {
                // Observe an extra byte without ever allocating an unbounded response buffer.
                int count = Math.min(chunk.remaining(), limit - bytes.size() + 1);
                byte[] copy = new byte[count];
                chunk.get(copy);
                bytes.writeBytes(copy);
                if (bytes.size() > limit) {
                    subscription.cancel();
                    body.completeExceptionally(new IOException("response body exceeds limit"));
                    return;
                }
            }
            subscription.request(1);
        }

        @Override
        public void onError(Throwable error) {
            body.completeExceptionally(error);
        }

        @Override
        public void onComplete() {
            body.complete(bytes.toByteArray());
        }
    }

    private static final class InsecureTrust extends X509ExtendedTrustManager {
        @Override public X509Certificate[] getAcceptedIssuers() { return new X509Certificate[0]; }
        @Override public void checkClientTrusted(X509Certificate[] c, String a) {}
        @Override public void checkServerTrusted(X509Certificate[] c, String a) {}
        @Override public void checkClientTrusted(X509Certificate[] c, String a, Socket s) {}
        @Override public void checkServerTrusted(X509Certificate[] c, String a, Socket s) {}
        @Override public void checkClientTrusted(X509Certificate[] c, String a, SSLEngine e) {}
        @Override public void checkServerTrusted(X509Certificate[] c, String a, SSLEngine e) {}
    }
}
