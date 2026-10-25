package de.buderus.bridge;

import com.fasterxml.jackson.core.JsonParser;
import com.fasterxml.jackson.core.JsonToken;
import com.fasterxml.jackson.databind.ObjectMapper;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.HashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.TreeMap;

final class Mapping {
    record Message(String topic, byte[] payload, boolean retain) {}

    private Mapping() {}

    static List<Message> map(byte[] body, String prefix, boolean retain, String mode) throws IOException {
        if (mode.equals("passthrough")) {
            return List.of(new Message(prefix, body.clone(), retain));
        }
        // Keep numeric token text verbatim, including decimal precision and exponent notation.
        ObjectMapper mapper = new ObjectMapper();
        Object value;
        try (JsonParser parser = mapper.getFactory().createParser(body)) {
            if (parser.nextToken() != JsonToken.START_OBJECT) {
                throw new IOException("expected json object");
            }
            value = read(parser, mapper);
            if (parser.nextToken() != null) {
                throw new IOException("expected exactly one json object");
            }
        } catch (IOException e) {
            // Parser diagnostics include response fragments; never propagate them to application logs.
            throw new IOException("invalid json object");
        }
        List<Message> messages = new ArrayList<>();
        walk(cast(value), prefix.replaceAll("/+$", ""), retain, messages, new HashSet<>(), mapper);
        return List.copyOf(messages);
    }

    private record NumberText(String text) {}

    private static Object read(JsonParser parser, ObjectMapper mapper) throws IOException {
        return switch (parser.currentToken()) {
            case START_OBJECT -> {
                Map<String, Object> object = new TreeMap<>((a, b) -> Arrays.compareUnsigned(
                        a.getBytes(StandardCharsets.UTF_8), b.getBytes(StandardCharsets.UTF_8)));
                while (parser.nextToken() != JsonToken.END_OBJECT) {
                    String name = parser.currentName();
                    parser.nextToken();
                    object.put(name, read(parser, mapper));
                }
                yield object;
            }
            case START_ARRAY -> {
                List<Object> array = new ArrayList<>();
                while (parser.nextToken() != JsonToken.END_ARRAY) {
                    array.add(read(parser, mapper));
                }
                yield array;
            }
            case VALUE_NUMBER_INT, VALUE_NUMBER_FLOAT -> new NumberText(parser.getText());
            case VALUE_STRING -> parser.getText();
            case VALUE_TRUE -> true;
            case VALUE_FALSE -> false;
            case VALUE_NULL -> null;
            default -> throw new IOException("invalid json value");
        };
    }

    @SuppressWarnings("unchecked")
    private static Map<String, Object> cast(Object value) {
        return (Map<String, Object>) value;
    }

    private static String json(Object value, ObjectMapper mapper) throws IOException {
        if (value instanceof NumberText number) {
            return number.text();
        }
        if (value instanceof Map<?, ?> object) {
            List<String> entries = new ArrayList<>();
            for (var entry : object.entrySet()) {
                entries.add(mapper.writeValueAsString(entry.getKey()) + ":" + json(entry.getValue(), mapper));
            }
            return "{" + String.join(",", entries) + "}";
        }
        if (value instanceof List<?> array) {
            List<String> entries = new ArrayList<>();
            for (Object item : array) {
                entries.add(json(item, mapper));
            }
            return "[" + String.join(",", entries) + "]";
        }
        return mapper.writeValueAsString(value);
    }

    private static void walk(Map<String, Object> object, String prefix, boolean retain,
            List<Message> messages, Set<String> seen, ObjectMapper mapper) throws IOException {
        for (var entry : object.entrySet()) {
            String part = entry.getKey().replaceAll("[/+# \\x00]", "_");
            if (part.isEmpty()) {
                part = "_";
            }
            String topic = prefix.isEmpty() ? part : prefix + "/" + part;
            if (!Settings.validTopic(topic) || !seen.add(topic)) {
                throw new IOException("invalid or colliding mapped topic");
            }
            if (entry.getValue() instanceof Map<?, ?> nested) {
                walk(cast(nested), topic, retain, messages, seen, mapper);
            } else {
                String payload = entry.getValue() instanceof String text ? text : json(entry.getValue(), mapper);
                messages.add(new Message(topic, payload.getBytes(StandardCharsets.UTF_8), retain));
            }
        }
    }
}
