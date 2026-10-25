package de.buderus.bridge;

import com.cronutils.model.Cron;
import com.cronutils.model.CronType;
import com.cronutils.model.definition.CronDefinitionBuilder;
import com.cronutils.model.time.ExecutionTime;
import com.cronutils.parser.CronParser;
import java.time.Duration;
import java.time.Instant;
import java.time.ZoneId;
import java.util.Map;
import java.util.Optional;

final class Plan {
    private static final Map<String, String> DESCRIPTORS = Map.of(
            "@yearly", "0 0 1 1 *", "@annually", "0 0 1 1 *", "@monthly", "0 0 1 * *",
            "@weekly", "0 0 * * 0", "@daily", "0 0 * * *", "@midnight", "0 0 * * *",
            "@hourly", "0 * * * *");
    private final Duration interval;
    private final ExecutionTime cron;
    private final ZoneId zone;

    private Plan(Duration interval, ExecutionTime cron, ZoneId zone) {
        this.interval = interval;
        this.cron = cron;
        this.zone = zone;
    }

    static Plan parse(String value, ZoneId zone) {
        try {
            if (value.startsWith("@every ")) {
                Duration duration = Settings.duration(value.substring(7), "SCHEDULE");
                // robfig/cron rounds intervals down to whole seconds, with a one-second minimum.
                return new Plan(Duration.ofSeconds(Math.max(1, duration.toSeconds())), null, zone);
            }
            String expression = DESCRIPTORS.getOrDefault(value, value);
            if (expression.trim().split("\\s+").length != 5) {
                throw new IllegalArgumentException();
            }
            Cron parsed = new CronParser(CronDefinitionBuilder.instanceDefinitionFor(CronType.UNIX))
                    .parse(expression);
            parsed.validate();
            return new Plan(null, ExecutionTime.forCron(parsed), zone);
        } catch (RuntimeException e) {
            throw new IllegalArgumentException("invalid SCHEDULE");
        }
    }

    Optional<Duration> delay(Instant now) {
        if (interval != null) {
            return Optional.of(interval.minusNanos(now.getNano()));
        }
        return cron.timeToNextExecution(now.atZone(zone));
    }
}
