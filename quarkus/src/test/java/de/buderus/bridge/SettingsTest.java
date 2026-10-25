package de.buderus.bridge;

import static org.junit.jupiter.api.Assertions.*;
import java.io.ByteArrayOutputStream;
import java.io.PrintStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.Path;
import java.time.Duration;
import java.util.HashMap;
import java.util.Map;
import io.smallrye.config.SmallRyeConfig;
import io.smallrye.config.SmallRyeConfigBuilder;
import io.smallrye.config.PropertiesConfigSource;
import org.junit.jupiter.api.Test;

class SettingsTest {
    static Map<String, String> env() {
        return new HashMap<>(Map.of("API_URL", "http://localhost/api"));
    }

    static BridgeConfig config(Map<String, String> values) {
        return properties(values).getConfigMapping(BridgeConfig.class);
    }

    static SmallRyeConfig properties(Map<String, String> values) {
        try {
            return new SmallRyeConfigBuilder()
                    .addDefaultInterceptors()
                    .withSources(new PropertiesConfigSource(
                            Path.of(System.getProperty("basedir", "."))
                                    .resolve("src/main/resources/application.properties").toUri().toURL(), 100),
                            new PropertiesConfigSource(values, "test", 300))
                    .withMapping(BridgeConfig.class)
                    .build();
        } catch (java.io.IOException e) {
            throw new IllegalStateException("cannot read application properties", e);
        }
    }

    static Settings settings(Map<String, String> values) {
        SmallRyeConfig properties = properties(values);
        return Settings.from(properties.getConfigMapping(BridgeConfig.class), properties);
    }

    @Test
    void defaultsAndEmptyFallback() {
        Map<String, String> env = env();
        env.put("API_URLS", "");
        Settings config = settings(env);
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
        env.put("API_URL", "${unused-url-expression}");
        env.put("API_URLS", "heating=http://localhost/a, water=https://localhost/b?q=a=b");
        assertEquals("water", settings(env).endpoints().get(1).name());
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
            Exception exception = assertThrows(IllegalArgumentException.class, () -> settings(env),
                    entry[0] + "=" + entry[1]);
            assertFalse(exception.getMessage().contains("secret"));
        }
        Map<String, String> env = env();
        env.putAll(Map.of("API_TOKEN", "placeholder", "API_USER", "user"));
        assertThrows(IllegalArgumentException.class, () -> settings(env));
    }

    @Test
    void goDurationSyntaxAndBooleanSyntax() {
        assertEquals(Duration.ofMillis(5_400_500),
                Settings.duration("1h30m.5s", "duration"));
        assertEquals(Duration.ofNanos(1001), Settings.duration("+1us1ns", "duration"));
        Map<String, String> env = env();
        env.put("POLL_ON_START", "0");
        assertFalse(settings(env).pollOnStart());
    }

    @Test
    void everyBooleanKeepsStrictGoSyntax() {
        for (String key : new String[] {
                "API_INSECURE_SKIP_VERIFY", "MQTT_AVAILABILITY", "MQTT_RETAIN", "POLL_ON_START"}) {
            for (String value : new String[] {"1", "t", "T", "TRUE", "true", "True",
                    "0", "f", "F", "FALSE", "false", "False"}) {
                Map<String, String> env = env();
                env.put(key, value);
                assertDoesNotThrow(() -> settings(env), key + "=" + value);
            }
            for (String value : new String[] {"", "yes", "on", " true", "True "}) {
                Map<String, String> env = env();
                env.put(key, value);
                assertThrows(IllegalArgumentException.class, () -> settings(env), key + "=" + value);
            }
        }
    }

    @Test
    void settingsAndMappingDoNotExposeCredentials() {
        Map<String, String> env = env();
        env.put("API_TOKEN", "private-token-placeholder");
        env.put("MQTT_PASSWORD", "private-password-placeholder");
        BridgeConfig mapping = config(env);
        Settings settings = settings(env);
        assertFalse(settings.toString().contains("private-"));
        assertFalse(mapping.toString().contains("private-"));
        assertEquals("private-token-placeholder", settings.apiToken());
        assertEquals("private-password-placeholder", settings.mqttPassword());
    }

    @Test
    void credentialsPreserveDollarSignsAndQuotes() {
        Map<String, String> values = env();
        values.put("API_USER", "placeholder");
        values.put("API_PASSWORD", "placeholder$with'quotes\"");
        values.put("MQTT_PASSWORD", "placeholder$with'quotes\"");
        Settings settings = settings(values);
        assertEquals(values.get("API_PASSWORD"), settings.apiPassword());
        assertEquals(values.get("MQTT_PASSWORD"), settings.mqttPassword());
    }

    @Test
    void sharedCredentialAliasesUseStandardQuarkusExpressionEscaping() {
        Map<String, String> values = env();
        values.put("API_USER", "literal\\${missing-user}");
        values.put("API_PASSWORD", "literal\\${missing-password}\\${key:fallback}$$'\"");
        values.put("MQTT_USER", "literal\\${missing-mqtt-user}");
        values.put("MQTT_PASSWORD", "literal\\${missing-password}\\${key:fallback}$$'\"");
        Settings settings = settings(values);
        assertEquals("literal${missing-user}", settings.apiUser());
        assertEquals("literal${missing-password}${key:fallback}$'\"", settings.apiPassword());
        assertEquals("literal${missing-mqtt-user}", settings.mqttUser());
        assertEquals("literal${missing-password}${key:fallback}$'\"", settings.mqttPassword());
        values.remove("API_USER");
        values.remove("API_PASSWORD");
        values.put("API_TOKEN", "literal\\${missing-token}\\${key:fallback}$$");
        assertEquals("literal${missing-token}${key:fallback}$", settings(values).apiToken());
    }

    @Test
    void standardCredentialPropertiesStillSupportQuarkusExpressions() {
        Map<String, String> values = env();
        values.put("bridge-sensitive.api-user", "placeholder");
        values.put("bridge-sensitive.api-password", "\\${placeholder}$$");
        values.put("bridge-sensitive.mqtt-password", "${test.password:configured}");
        Settings settings = settings(values);
        assertEquals("${placeholder}$", settings.apiPassword());
        assertEquals("configured", settings.mqttPassword());
    }

    @Test
    void unresolvedSensitiveExpressionsAreDeferredAndSanitized() {
        for (String key : new String[] {
                "API_URL", "API_URLS", "API_TOKEN", "API_USER", "API_PASSWORD", "MQTT_USER", "MQTT_PASSWORD"}) {
            Map<String, String> values = env();
            values.put(key, "prefix${private-fragment}suffix");
            assertDoesNotThrow(() -> properties(values));
            IllegalArgumentException error = assertThrows(IllegalArgumentException.class, () -> settings(values));
            assertEquals("invalid sensitive configuration", error.getMessage());
            assertNull(error.getCause());
        }
    }

    @Test
    void explicitStandardPropertiesOverrideEnvironmentAliases() {
        Map<String, String> values = env();
        values.put("API_TIMEOUT", "invalid");
        values.put("bridge.api-timeout", "1h30m.5s");
        values.put("bridge-sensitive.api-url", "https://localhost/overridden");
        Settings settings = settings(values);
        assertEquals(Duration.ofMillis(5_400_500), settings.apiTimeout());
        assertEquals("https://localhost/overridden", settings.endpoints().getFirst().uri().toString());
    }

    @Test
    void boundsAuthenticationAndConfiguredLoggingAreValidatedSeparately() {
        Map<String, String> values = env();
        values.put("API_MAX_BODY_BYTES", "2147483646");
        values.put("MQTT_QOS", "2");
        values.put("API_USER", "placeholder");
        values.put("API_PASSWORD", "placeholder");
        Settings settings = settings(values);
        assertEquals(2147483646, settings.maxBody());
        assertEquals(2, settings.qos());
        values.put("API_URL", "invalid");
        values.put("LOG_LEVEL", "warn");
        values.put("LOG_FORMAT", "text");
        BridgeConfig config = config(values);
        assertEquals("warn", Settings.logLevel(config));
        assertEquals("text", Settings.logFormat(config));
        assertThrows(IllegalArgumentException.class, () -> settings(values));
    }

    @Test
    void startupRejectionUsesValidatedLoggingWithoutLeakingValues() {
        Map<String, String> values = env();
        values.put("API_URL", "private-invalid-url-placeholder");
        values.put("API_TOKEN", "private\\${missing-token}\\${key:fallback}");
        values.put("MQTT_PASSWORD", "private\\${missing-password}\\${key:fallback}");
        values.put("LOG_FORMAT", "text");
        assertTrue(rejectedStartup(values).startsWith("time="));
        values.put("LOG_LEVEL", "private-invalid-level-placeholder");
        String output = rejectedStartup(values);
        assertTrue(output.startsWith("{"));
        assertTrue(output.contains("configuration rejected"));
        assertFalse(output.contains("private-"));
    }

    private static String rejectedStartup(Map<String, String> values) {
        Main main = new Main();
        main.bridge = new Bridge((Settings) null, null, null);
        main.config = config(values);
        main.settings = () -> settings(values);
        ByteArrayOutputStream output = new ByteArrayOutputStream();
        PrintStream original = System.out;
        try (PrintStream capture = new PrintStream(output, true, StandardCharsets.UTF_8)) {
            System.setOut(capture);
            assertEquals(1, main.run());
        } finally {
            System.setOut(original);
        }
        return output.toString(StandardCharsets.UTF_8);
    }
}
