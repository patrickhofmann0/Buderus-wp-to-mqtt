package de.buderus.bridge;

import static org.junit.jupiter.api.Assertions.*;
import java.time.Duration;
import java.util.HashMap;
import java.util.Map;
import org.junit.jupiter.api.Test;

class SettingsTest {
    static Map<String, String> env() {
        return new HashMap<>(Map.of("API_URL", "http://localhost/api"));
    }

    @Test
    void defaultsAndEmptyFallback() {
        Map<String, String> env = env();
        env.put("API_URLS", "");
        Settings config = Settings.load(env);
        assertEquals("http://localhost/api", config.endpoints().getFirst().uri().toString());
        assertEquals(Duration.ofSeconds(15), config.apiTimeout());
        assertEquals(1048576, config.maxBody());
        assertEquals(Duration.ofSeconds(5), config.mqttTimeout());
        assertEquals("tcp://localhost:1883", config.broker());
        assertEquals("buderus/wp/status", config.availabilityTopic());
        assertTrue(config.pollOnStart());
        assertEquals("fields", config.mapping());
        assertEquals(0, config.qos());
    }

    @Test
    void namedEndpointsHavePriorityEvenForOneEntry() {
        Map<String, String> env = env();
        env.put("API_URL", "invalid");
        env.put("API_URLS", "heating=http://localhost/a, water=https://localhost/b?q=a=b");
        assertEquals("water", Settings.load(env).endpoints().get(1).name());
    }

    @Test
    void rejectInvalidInputsWithoutEchoingValues() {
        String[][] cases = {
                {"API_URL", "******localhost"}, {"API_URL", "ftp://localhost"},
                {"API_URL", "http://localhost/#secret"}, {"API_URL", "http://localhost:0"},
                {"API_URLS", "x=http://localhost,x=http://localhost"}, {"API_URLS", "a/=http://localhost"},
                {"API_TIMEOUT", "0s"}, {"API_TIMEOUT", "1d"}, {"API_TIMEOUT", "secret"},
                {"API_MAX_BODY_BYTES", "2147483647"}, {"API_MAX_BODY_BYTES", "0"},
                {"MQTT_QOS", "3"}, {"MQTT_BROKER", "tcp://secret@localhost"},
                {"MQTT_TOPIC", "test/#"}, {"MQTT_TOPIC", ""}, {"MQTT_CLIENT_ID", ""},
                {"MQTT_PUBLISH_TIMEOUT", "-1s"}, {"MQTT_AVAILABILITY", "yes"},
                {"MAPPING", "unknown"}, {"TZ", ""}, {"TZ", "secret"},
                {"SCHEDULE", "0 0 0 * * *"}, {"SCHEDULE", "@every 0s"},
                {"SCHEDULE", "@every -1s"}, {"LOG_LEVEL", "trace"}, {"LOG_FORMAT", "unknown"},
                {"API_TOKEN", "secret\nvalue"}, {"API_USER", "secret:user"},
                {"API_PASSWORD", "secret"}
        };
        for (String[] entry : cases) {
            Map<String, String> env = env();
            env.put(entry[0], entry[1]);
            Exception exception = assertThrows(IllegalArgumentException.class, () -> Settings.load(env),
                    entry[0] + "=" + entry[1]);
            assertFalse(exception.getMessage().contains("secret"));
        }
        Map<String, String> env = env();
        env.putAll(Map.of("API_TOKEN", "placeholder", "API_USER", "user"));
        assertThrows(IllegalArgumentException.class, () -> Settings.load(env));
    }

    @Test
    void goDurationSyntaxAndBooleanSyntax() {
        assertEquals(Duration.ofMillis(5_400_500),
                Settings.duration("1h30m.5s", "duration"));
        assertEquals(Duration.ofNanos(1001), Settings.duration("+1us1ns", "duration"));
        Map<String, String> env = env();
        env.put("POLL_ON_START", "0");
        assertFalse(Settings.load(env).pollOnStart());
    }
}
