package de.buderus.bridge;

import java.math.BigDecimal;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.time.ZoneId;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

record Settings(List<Endpoint> endpoints, String apiToken, String apiUser, String apiPassword,
        Duration apiTimeout, int maxBody, boolean insecure, String broker, String clientId,
        String mqttUser, String mqttPassword, String topic, int qos, boolean retain,
        Duration mqttTimeout, boolean availability, String mapping, Plan plan, boolean pollOnStart,
        String logLevel, String logFormat) {
    record Endpoint(String name, URI uri) {}

    static Settings load(Map<String, String> env) {
        List<Endpoint> endpoints = new ArrayList<>();
        String urls = env.getOrDefault("API_URLS", "");
        if (!urls.isEmpty()) {
            Set<String> names = new HashSet<>();
            for (String item : urls.split(",", -1)) {
                String[] entry = item.trim().split("=", 2);
                require(entry.length == 2 && entry[0].trim().matches("[A-Za-z0-9_-]+")
                        && names.add(entry[0].trim()), "API_URLS");
                endpoints.add(new Endpoint(entry[0].trim(), uri(entry[1].trim(), false, "API_URLS")));
            }
        } else {
            endpoints.add(new Endpoint("", uri(env.getOrDefault("API_URL", ""), false, "API_URL")));
        }
        String token = env.getOrDefault("API_TOKEN", "");
        String user = env.getOrDefault("API_USER", "");
        String password = env.getOrDefault("API_PASSWORD", "");
        require(token.isEmpty() || (user.isEmpty() && password.isEmpty()), "api authentication");
        require(!control(token) && !control(user) && !control(password) && !token.contains(" ")
                && !user.contains(":") && (password.isEmpty() || !user.isEmpty()), "api authentication");
        Duration apiTimeout = duration(env.getOrDefault("API_TIMEOUT", "15s"), "API_TIMEOUT");
        Duration mqttTimeout = duration(env.getOrDefault("MQTT_PUBLISH_TIMEOUT", "5s"), "MQTT_PUBLISH_TIMEOUT");
        int maxBody = integer(env.getOrDefault("API_MAX_BODY_BYTES", "1048576"), 1, Integer.MAX_VALUE - 1,
                "API_MAX_BODY_BYTES");
        String broker = env.getOrDefault("MQTT_BROKER", "tcp://localhost:1883");
        uri(broker, true, "MQTT_BROKER");
        String clientId = env.getOrDefault("MQTT_CLIENT_ID", "buderus-wp-to-mqtt");
        String mqttUser = env.getOrDefault("MQTT_USER", "");
        String mqttPassword = env.getOrDefault("MQTT_PASSWORD", "");
        require(!clientId.isEmpty() && mqttString(clientId) && mqttString(mqttUser)
                && mqttPassword.getBytes(StandardCharsets.UTF_8).length <= 65535, "mqtt credentials");
        String topic = env.getOrDefault("MQTT_TOPIC", "buderus/wp");
        require(validTopic(topic), "MQTT_TOPIC");
        boolean availability = bool(env, "MQTT_AVAILABILITY", "true");
        require(!availability || validTopic(topic + "/status"), "MQTT_TOPIC");
        String mapping = env.getOrDefault("MAPPING", "fields");
        require(Set.of("fields", "passthrough").contains(mapping), "MAPPING");
        ZoneId zone;
        try {
            zone = ZoneId.of(env.getOrDefault("TZ", "UTC"));
        } catch (RuntimeException e) {
            throw invalid("TZ");
        }
        Plan plan = Plan.parse(env.getOrDefault("SCHEDULE", "@every 60s"), zone);
        String level = env.getOrDefault("LOG_LEVEL", "info");
        String format = env.getOrDefault("LOG_FORMAT", "json");
        require(Set.of("debug", "info", "warn", "error").contains(level), "LOG_LEVEL");
        require(Set.of("json", "text").contains(format), "LOG_FORMAT");
        return new Settings(List.copyOf(endpoints), token, user, password, apiTimeout, maxBody,
                bool(env, "API_INSECURE_SKIP_VERIFY", "false"), broker, clientId, mqttUser, mqttPassword,
                topic, integer(env.getOrDefault("MQTT_QOS", "0"), 0, 2, "MQTT_QOS"),
                bool(env, "MQTT_RETAIN", "false"), mqttTimeout, availability, mapping, plan,
                bool(env, "POLL_ON_START", "true"), level, format);
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

    private static boolean bool(Map<String, String> env, String key, String fallback) {
        return switch (env.getOrDefault(key, fallback)) {
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
