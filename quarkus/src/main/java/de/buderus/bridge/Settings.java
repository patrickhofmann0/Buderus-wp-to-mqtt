package de.buderus.bridge;

import io.smallrye.config.ConfigValue;
import io.smallrye.config.SmallRyeConfig;
import java.math.BigDecimal;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.time.ZoneId;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.List;
import java.util.Set;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import org.eclipse.microprofile.config.Config;

record Settings(List<Endpoint> endpoints, String apiToken, String apiUser, String apiPassword,
        Duration apiTimeout, int maxBody, boolean insecure, String broker, String clientId,
        String mqttUser, String mqttPassword, String topic, int qos, boolean retain,
        Duration mqttTimeout, boolean availability, String mapping, Plan plan, boolean pollOnStart,
        String logLevel, String logFormat) {
    record Endpoint(String name, URI uri) {}

    static Settings from(BridgeConfig config, Config properties) {
        List<Endpoint> endpoints = new ArrayList<>();
        String urls = sensitive(properties, "bridge-sensitive.api-urls");
        if (!urls.isEmpty()) {
            Set<String> names = new HashSet<>();
            for (String item : urls.split(",", -1)) {
                String[] entry = item.trim().split("=", 2);
                require(entry.length == 2 && entry[0].trim().matches("[A-Za-z0-9_-]+")
                        && names.add(entry[0].trim()), "API_URLS");
                endpoints.add(new Endpoint(entry[0].trim(), uri(entry[1].trim(), false, "API_URLS")));
            }
        } else {
            endpoints.add(new Endpoint("", uri(sensitive(properties, "bridge-sensitive.api-url"), false, "API_URL")));
        }
        String token = sensitive(properties, "bridge-sensitive.api-token");
        String user = sensitive(properties, "bridge-sensitive.api-user");
        String password = sensitive(properties, "bridge-sensitive.api-password");
        require(token.isEmpty() || (user.isEmpty() && password.isEmpty()), "api authentication");
        require(!control(token) && !control(user) && !control(password) && !token.contains(" ")
                && !user.contains(":") && (password.isEmpty() || !user.isEmpty()), "api authentication");
        Duration apiTimeout = duration(config.apiTimeout().orElse(""), "API_TIMEOUT");
        Duration mqttTimeout = duration(config.mqttPublishTimeout().orElse(""), "MQTT_PUBLISH_TIMEOUT");
        int maxBody = integer(config.apiMaxBodyBytes().orElse(""), 1, Integer.MAX_VALUE - 1,
                "API_MAX_BODY_BYTES");
        String broker = config.mqttBroker().orElse("");
        uri(broker, true, "MQTT_BROKER");
        String clientId = config.mqttClientId().orElse("");
        String mqttUser = sensitive(properties, "bridge-sensitive.mqtt-user");
        String mqttPassword = sensitive(properties, "bridge-sensitive.mqtt-password");
        require(!clientId.isEmpty() && mqttString(clientId) && mqttString(mqttUser)
                && mqttPassword.getBytes(StandardCharsets.UTF_8).length <= 65535, "mqtt credentials");
        String topic = config.mqttTopic().orElse("");
        require(validTopic(topic), "MQTT_TOPIC");
        boolean availability = bool(config.mqttAvailability().orElse(""), "MQTT_AVAILABILITY");
        require(!availability || validTopic(topic + "/status"), "MQTT_TOPIC");
        String mapping = config.mapping().orElse("");
        require(Set.of("fields", "passthrough").contains(mapping), "MAPPING");
        ZoneId zone;
        try {
            zone = ZoneId.of(config.timezone().orElse(""));
        } catch (RuntimeException e) {
            throw invalid("TZ");
        }
        Plan plan = Plan.parse(config.schedule().orElse(""), zone);
        String level = logLevel(config);
        String format = logFormat(config);
        return new Settings(List.copyOf(endpoints), token, user, password, apiTimeout, maxBody,
                bool(config.apiInsecureSkipVerify().orElse(""), "API_INSECURE_SKIP_VERIFY"),
                broker, clientId, mqttUser, mqttPassword,
                topic, integer(config.mqttQos().orElse(""), 0, 2, "MQTT_QOS"),
                bool(config.mqttRetain().orElse(""), "MQTT_RETAIN"), mqttTimeout, availability, mapping, plan,
                bool(config.pollOnStart().orElse(""), "POLL_ON_START"), level, format);
    }

    static String logLevel(BridgeConfig config) {
        String level = config.logLevel().orElse("");
        require(Set.of("debug", "info", "warn", "error").contains(level), "LOG_LEVEL");
        return level;
    }

    private static String sensitive(Config properties, String key) {
        try {
            // Optional lookups can hide unresolved expressions as absent values.
            ConfigValue value = properties.unwrap(SmallRyeConfig.class).getConfigValue(key);
            require(!value.hasProblems(), "sensitive configuration");
            return value.getValue() == null ? "" : value.getValue();
        } catch (RuntimeException e) {
            throw invalid("sensitive configuration");
        }
    }

    static String logFormat(BridgeConfig config) {
        String format = config.logFormat().orElse("");
        require(Set.of("json", "text").contains(format), "LOG_FORMAT");
        return format;
    }

    @Override
    public String toString() {
        return "Settings[redacted]";
    }

    String availabilityTopic() {
        return availability ? topic + "/status" : "";
    }

    static Duration duration(String value, String key) {
        // Go durations allow mixed, fractional units, but not calendar days.
        Pattern part = Pattern.compile("(\\d+(?:\\.\\d*)?|\\.\\d+)(ns|us|µs|μs|ms|s|m|h)");
        Matcher matcher = part.matcher(value.startsWith("+") ? value.substring(1) : value);
        BigDecimal nanos = BigDecimal.ZERO;
        int end = 0;
        while (matcher.find()) {
            require(matcher.start() == end, key);
            long multiplier = switch (matcher.group(2)) {
                case "ns" -> 1;
                case "us", "µs", "μs" -> 1_000;
                case "ms" -> 1_000_000;
                case "s" -> 1_000_000_000;
                case "m" -> 60_000_000_000L;
                default -> 3_600_000_000_000L;
            };
            nanos = nanos.add(new BigDecimal(matcher.group(1)).multiply(BigDecimal.valueOf(multiplier)));
            end = matcher.end();
        }
        String unsigned = value.startsWith("+") ? value.substring(1) : value;
        require(end == unsigned.length() && end > 0, key);
        try {
            long n = nanos.toBigInteger().longValueExact();
            require(n > 0, key);
            return Duration.ofNanos(n);
        } catch (ArithmeticException e) {
            throw invalid(key);
        }
    }

    private static URI uri(String value, boolean mqtt, String key) {
        try {
            URI uri = URI.create(value);
            Set<String> schemes = mqtt ? Set.of("tcp", "mqtt", "ssl", "tls", "mqtts", "ws", "wss")
                    : Set.of("http", "https");
            require(uri.getScheme() != null && schemes.contains(uri.getScheme()) && uri.getHost() != null
                    && uri.getRawUserInfo() == null && uri.getFragment() == null
                    && uri.getPort() != 0 && uri.getPort() <= 65535
                    && !uri.getRawAuthority().endsWith(":"), key);
            return uri;
        } catch (IllegalArgumentException e) {
            throw invalid(key);
        }
    }

    static boolean validTopic(String topic) {
        return !topic.isEmpty() && mqttString(topic) && !topic.contains("+") && !topic.contains("#");
    }

    private static boolean mqttString(String value) {
        return value.getBytes(StandardCharsets.UTF_8).length <= 65535 && value.indexOf('\0') < 0
                && StandardCharsets.UTF_8.newEncoder().canEncode(value);
    }

    private static boolean control(String value) {
        return value.chars().anyMatch(c -> c < 32 || c == 127);
    }

    private static boolean bool(String value, String key) {
        return switch (value) {
            case "1", "t", "T", "TRUE", "true", "True" -> true;
            case "0", "f", "F", "FALSE", "false", "False" -> false;
            default -> throw invalid(key);
        };
    }

    private static int integer(String value, int min, int max, String key) {
        try {
            int number = Integer.parseInt(value);
            require(number >= min && number <= max, key);
            return number;
        } catch (NumberFormatException e) {
            throw invalid(key);
        }
    }

    private static void require(boolean valid, String key) {
        if (!valid) {
            throw invalid(key);
        }
    }

    private static IllegalArgumentException invalid(String key) {
        return new IllegalArgumentException("invalid " + key);
    }
}
