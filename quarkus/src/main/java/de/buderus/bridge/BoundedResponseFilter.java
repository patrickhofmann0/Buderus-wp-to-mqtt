package de.buderus.bridge;

import io.vertx.core.http.HttpClientRequest;
import jakarta.ws.rs.client.ClientResponseContext;
import java.io.ByteArrayInputStream;
import java.io.IOException;
import org.jboss.resteasy.reactive.client.spi.ResteasyReactiveClientRequestContext;
import org.jboss.resteasy.reactive.client.spi.ResteasyReactiveClientResponseFilter;

final class BoundedResponseFilter implements ResteasyReactiveClientResponseFilter {
    private final int limit;

    BoundedResponseFilter(int limit) {
        this.limit = limit;
    }

    @Override
    public void filter(ResteasyReactiveClientRequestContext request, ClientResponseContext response) {
        int status = response.getStatus();
        if (status < 200 || status >= 300) {
            request.unwrap(HttpClientRequest.class).reset();
            throw new StatusException(status);
        }
        // Keep the Uni pending until the bounded stream has been consumed, so cancellation
        // resets the HTTP request during both header and body reads.
        request.suspend();
        request.getContext().executeBlocking(() -> {
            try (var body = response.getEntityStream()) {
                byte[] bytes = body == null ? new byte[0] : body.readNBytes(limit + 1);
                if (bytes.length > limit) {
                    request.unwrap(HttpClientRequest.class).reset();
                    throw new BodyLimitException();
                }
                response.setEntityStream(new ByteArrayInputStream(bytes));
            } catch (IOException e) {
                request.unwrap(HttpClientRequest.class).reset();
                throw e;
            }
            return null;
        }, false).onComplete(result -> {
            if (result.succeeded()) {
                request.resume();
            } else {
                request.resume(result.cause());
            }
        });
    }

    static final class BodyLimitException extends RuntimeException {}

    static final class StatusException extends RuntimeException {
        final int status;

        StatusException(int status) {
            super("http status " + status);
            this.status = status;
        }
    }
}
