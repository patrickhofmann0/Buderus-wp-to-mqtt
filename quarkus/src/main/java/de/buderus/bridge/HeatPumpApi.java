package de.buderus.bridge;

import io.quarkus.rest.client.reactive.Url;
import io.smallrye.mutiny.Uni;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.HeaderParam;
import java.io.Closeable;
import java.io.InputStream;
import java.net.URI;
import org.eclipse.microprofile.rest.client.inject.RegisterRestClient;

@RegisterRestClient(configKey = "buderus-api")
public interface HeatPumpApi extends Closeable {
    @GET
    Uni<InputStream> fetch(@Url URI endpoint, @HeaderParam("Authorization") String authorization);
}
