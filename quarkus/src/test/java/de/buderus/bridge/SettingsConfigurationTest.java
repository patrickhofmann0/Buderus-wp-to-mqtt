package de.buderus.bridge;

import static org.junit.jupiter.api.Assertions.*;

import io.quarkus.test.junit.QuarkusTest;
import io.quarkus.test.junit.QuarkusTestProfile;
import io.quarkus.test.junit.TestProfile;
import jakarta.inject.Inject;
import java.time.Duration;
import java.util.Map;
import org.junit.jupiter.api.Test;

@QuarkusTest
@TestProfile(SettingsConfigurationTest.ConfigurationProfile.class)
class SettingsConfigurationTest {
    @Inject
    BridgeConfig config;

    @Inject
    Settings settings;

    public static class ConfigurationProfile implements QuarkusTestProfile {
        @Override
        public Map<String, String> getConfigOverrides() {
            return Map.of("API_URL", "http://localhost/config-test", "API_URLS", "",
                    "API_TIMEOUT", "1m2.5s", "MQTT_RETAIN", "1", "POLL_ON_START", "0",
                    "bridge.mqtt-client-id", "configuration-test",
                    "API_USER", "literal\\${missing-user}",
                    "API_PASSWORD", "literal\\${missing-password}\\${key:fallback}$$",
                    "MQTT_USER", "literal\\${missing-mqtt-user}",
                    "MQTT_PASSWORD", "literal\\${missing-password}\\${key:fallback}$$");
        }
    }

    @Test
    void cdiMappingResolvesSharedAliasesAndStandardProperties() {
        assertEquals("configuration-test", config.mqttClientId().orElseThrow());
        assertEquals("http://localhost/config-test", settings.endpoints().getFirst().uri().toString());
        assertEquals(Duration.ofMillis(62_500), settings.apiTimeout());
        assertEquals("configuration-test", settings.clientId());
        assertEquals(Duration.ofSeconds(5), settings.mqttTimeout());
        assertTrue(settings.retain());
        assertFalse(settings.pollOnStart());
        assertEquals("literal${missing-user}", settings.apiUser());
        assertEquals("literal${missing-password}${key:fallback}$", settings.apiPassword());
        assertEquals("literal${missing-mqtt-user}", settings.mqttUser());
        assertEquals("literal${missing-password}${key:fallback}$", settings.mqttPassword());
    }

    @Test
    void isolatedMappingDoesNotUseAugmentedQuarkusTestResources() {
        Settings isolated = SettingsTest.settings(Map.of("API_URL", "http://localhost/isolated",
                "MQTT_CLIENT_ID", "isolated", "API_TIMEOUT", "2s"));
        assertEquals("http://localhost/isolated", isolated.endpoints().getFirst().uri().toString());
        assertEquals("isolated", isolated.clientId());
        assertEquals(Duration.ofSeconds(2), isolated.apiTimeout());
    }
}
