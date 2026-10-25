package de.buderus.bridge;

import static org.junit.jupiter.api.Assertions.*;
import java.time.Duration;
import java.time.Instant;
import java.time.ZoneId;
import org.junit.jupiter.api.Test;

class PlanTest {
    @Test
    void descriptorsIntervalsTimezoneAndUnixDayOrSemantics() {
        Instant now = Instant.parse("2026-01-01T00:00:00Z");
        assertEquals(Duration.ofHours(1), Plan.parse("@hourly", ZoneId.of("UTC")).delay(now).orElseThrow());
        assertEquals(Duration.ofSeconds(60), Plan.parse("@every 1m", ZoneId.of("UTC")).delay(now).orElseThrow());
        assertEquals(Duration.ofSeconds(1), Plan.parse("@every .5s", ZoneId.of("UTC")).delay(now).orElseThrow());
        assertEquals(Duration.ofMinutes(5), Plan.parse("*/5 * * * *", ZoneId.of("UTC")).delay(now).orElseThrow());
        assertEquals(Duration.ofHours(23), Plan.parse("@daily", ZoneId.of("Europe/Berlin")).delay(now).orElseThrow());
        // UNIX cron fires on day-of-month OR day-of-week when both fields are restricted.
        assertEquals(Duration.ofDays(1), Plan.parse("0 0 2 * MON", ZoneId.of("UTC")).delay(now).orElseThrow());
        for (String descriptor : new String[] {"@yearly", "@annually", "@monthly", "@weekly",
                "@daily", "@midnight", "@hourly"}) {
            assertFalse(Plan.parse(descriptor, ZoneId.of("UTC")).delay(now).orElseThrow().isNegative());
        }
    }
}
