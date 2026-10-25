package de.buderus.bridge;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;
import java.time.Instant;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

final class Events {
    private static final List<String> LEVELS = List.of("debug", "info", "warn", "error");
    private static final ObjectMapper JSON = new ObjectMapper();
    private final String level;
    private final String format;

    Events(String level, String format) {
        this.level = level;
        this.format = format;
    }

    synchronized void log(String severity, String message) {
        if (LEVELS.indexOf(severity) < LEVELS.indexOf(level)) {
            return;
        }
        Map<String, String> fields = new LinkedHashMap<>();
        fields.put("time", Instant.now().toString());
        fields.put("level", severity);
        fields.put("msg", message);
        try {
            if (format.equals("json")) {
                System.out.println(JSON.writeValueAsString(fields));
            } else {
                System.out.printf("time=%s level=%s msg=%s%n", fields.get("time"), severity,
                        JSON.writeValueAsString(message));
            }
        } catch (JsonProcessingException e) {
            System.out.println("{\"level\":\"error\",\"msg\":\"log encoding failed\"}");
        }
    }
}
