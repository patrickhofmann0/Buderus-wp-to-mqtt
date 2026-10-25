package de.buderus.bridge;

import static org.junit.jupiter.api.Assertions.*;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.util.List;
import org.junit.jupiter.api.Test;

class MappingTest {
    private List<Mapping.Message> map(String json) throws IOException {
        return Mapping.map(json.getBytes(StandardCharsets.UTF_8), "test/", true, "fields");
    }

    private String payload(Mapping.Message message) {
        return new String(message.payload(), StandardCharsets.UTF_8);
    }

    @Test
    void sortedRecursivePreciseAndSanitized() throws Exception {
        var messages = map("""
                {"z":null,"n":{"b":true,"a":9007199254740993123456789},"d":1.00000000000000001,
                "a":[{"z":1.2300,"a":null},2],"raw":"text","a/b+# \\u0000":false,"":7}
                """);
        assertEquals(List.of("test/_", "test/a", "test/a_b____", "test/d", "test/n/a",
                "test/n/b", "test/raw", "test/z"), messages.stream().map(Mapping.Message::topic).toList());
        assertEquals("[{\"a\":null,\"z\":1.2300},2]", payload(messages.get(1)));
        assertEquals("1.00000000000000001", payload(messages.get(3)));
        assertEquals("9007199254740993123456789", payload(messages.get(4)));
        assertEquals("text", payload(messages.get(6)));
        assertEquals("null", payload(messages.get(7)));
        assertTrue(messages.stream().allMatch(Mapping.Message::retain));
    }

    @Test
    void rejectsCollisionsAndInvalidDocumentsAtomically() {
        for (String value : List.of("{\"a/b\":1,\"a_b\":2}", "{\"a/b\":{},\"a_b\":2}", "[]", "null",
                "{\"secret\":", "{} {}", "{\"a\":NaN}", "{\"a\":1} trailing")) {
            IOException error = assertThrows(IOException.class, () -> map(value), value);
            assertFalse(error.getMessage().contains("secret"));
        }
    }

    @Test
    void passthroughDoesNotInterpretBytes() throws Exception {
        byte[] bytes = {0, 1, -1};
        var messages = Mapping.map(bytes, "test", false, "passthrough");
        assertArrayEquals(bytes, messages.getFirst().payload());
        assertFalse(messages.getFirst().retain());
        assertTrue(map("{}").isEmpty());
    }

    @Test
    void keysUseUtf8OrderRatherThanUtf16Order() throws Exception {
        var messages = map("{\"😀\":1,\"\\ue000\":2}");
        assertEquals("test/\ue000", messages.getFirst().topic());
        assertEquals("test/😀", messages.getLast().topic());
    }
}
