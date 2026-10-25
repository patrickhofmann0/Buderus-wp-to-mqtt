package de.buderus.bridge;

import static org.junit.jupiter.api.Assertions.*;
import com.fasterxml.jackson.databind.ObjectMapper;
import java.io.ByteArrayOutputStream;
import java.io.PrintStream;
import java.nio.charset.StandardCharsets;
import org.junit.jupiter.api.Test;

class EventsTest {
    @Test
    void jsonTextLevelAndEscaping() throws Exception {
        PrintStream original = System.out;
        ByteArrayOutputStream bytes = new ByteArrayOutputStream();
        try (PrintStream capture = new PrintStream(bytes, true, StandardCharsets.UTF_8)) {
            System.setOut(capture);
            Events json = new Events("warn", "json");
            json.log("info", "must be filtered");
            json.log("error", "line\nbreak");
            String record = bytes.toString(StandardCharsets.UTF_8).trim();
            assertFalse(record.contains("must be filtered"));
            var object = new ObjectMapper().readTree(record);
            assertEquals("error", object.get("level").asText());
            assertEquals("line\nbreak", object.get("msg").asText());
            bytes.reset();
            new Events("debug", "text").log("info", "line\nbreak");
            String text = bytes.toString(StandardCharsets.UTF_8);
            assertEquals(1, text.lines().count());
            assertTrue(text.contains("level=info"));
            assertTrue(text.contains("msg=\"line\\nbreak\""));
        } finally {
            System.setOut(original);
        }
    }
}
