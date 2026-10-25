package de.buderus.bridge;

import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Produces;
import jakarta.inject.Singleton;
import org.eclipse.microprofile.config.Config;

@ApplicationScoped
class SettingsConfiguration {
    @Produces
    @Singleton
    Settings settings(BridgeConfig config, Config properties) {
        return Settings.from(config, properties);
    }
}
