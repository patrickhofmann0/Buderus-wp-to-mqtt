package de.buderus.bridge;

import io.quarkus.runtime.Quarkus;
import io.quarkus.runtime.QuarkusApplication;
import io.quarkus.runtime.annotations.QuarkusMain;
import jakarta.enterprise.event.Observes;
import jakarta.inject.Inject;
import jakarta.inject.Provider;
import io.quarkus.runtime.ShutdownEvent;

@QuarkusMain
public class Main implements QuarkusApplication {
    @Inject
    Bridge bridge;

    @Inject
    BridgeConfig config;

    @Inject
    Provider<Settings> settings;

    @Override
    public int run(String... args) {
        Events events = new Events("info", "json");
        try {
            events = new Events(Settings.logLevel(config), Settings.logFormat(config));
            try {
                settings.get();
            } catch (RuntimeException e) {
                events.log("error", "configuration rejected");
                return 1;
            }
            bridge.start();
            Quarkus.waitForExit();
            return 0;
        } catch (IllegalArgumentException e) {
            events.log("error", "configuration rejected");
            return 1;
        } catch (Exception e) {
            events.log("error", "service startup failed");
            return 1;
        } finally {
            bridge.close();
        }
    }

    void shutdown(@Observes ShutdownEvent event) {
        bridge.close();
    }
}
