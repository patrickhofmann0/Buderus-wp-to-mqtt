package de.buderus.bridge;

import io.quarkus.runtime.Quarkus;
import io.quarkus.runtime.QuarkusApplication;
import io.quarkus.runtime.annotations.QuarkusMain;
import jakarta.enterprise.event.Observes;
import jakarta.inject.Inject;
import io.quarkus.runtime.ShutdownEvent;

@QuarkusMain
public class Main implements QuarkusApplication {
    @Inject
    Bridge bridge;

    @Override
    public int run(String... args) {
        try {
            Settings settings = Settings.load(System.getenv());
            bridge.start(settings);
            Quarkus.waitForExit();
            return 0;
        } catch (IllegalArgumentException e) {
            new Events("info", "json").log("error", "configuration rejected");
            return 1;
        } catch (Exception e) {
            new Events("info", "json").log("error", "service startup failed");
            return 1;
        } finally {
            bridge.close();
        }
    }

    void shutdown(@Observes ShutdownEvent event) {
        bridge.close();
    }
}
