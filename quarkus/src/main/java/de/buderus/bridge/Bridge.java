package de.buderus.bridge;

import jakarta.enterprise.context.ApplicationScoped;
import java.io.IOException;
import java.security.GeneralSecurityException;
import java.time.Instant;
import java.util.List;
import java.util.concurrent.ArrayBlockingQueue;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.ThreadPoolExecutor;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import org.eclipse.paho.client.mqttv3.MqttException;

@ApplicationScoped
public class Bridge implements AutoCloseable {
    private final AtomicBoolean running = new AtomicBoolean();
    private final AtomicBoolean stopped = new AtomicBoolean();
    private final ScheduledExecutorService timer = Executors.newSingleThreadScheduledExecutor();
    private final ThreadPoolExecutor worker = new ThreadPoolExecutor(1, 1, 0, TimeUnit.SECONDS,
            new ArrayBlockingQueue<>(1), new ThreadPoolExecutor.DiscardPolicy());
    private Settings settings;
    private Events events;
    private RestSource rest;
    private MqttSink mqtt;

    void start(Settings settings) throws GeneralSecurityException, MqttException {
        this.settings = settings;
        events = new Events(settings.logLevel(), settings.logFormat());
        if (settings.insecure()) {
            events.log("warn", "API TLS verification disabled; local exception permits man-in-the-middle attacks");
        }
        rest = new RestSource(settings);
        mqtt = new MqttSink(settings, events);
        mqtt.connect();
        scheduleNext();
        if (settings.pollOnStart()) {
            trigger();
        }
        events.log("info", "service started");
    }

    private void scheduleNext() {
        if (stopped.get()) {
            return;
        }
        var delay = settings.plan().delay(Instant.now());
        if (delay.isEmpty()) {
            return;
        }
        long nanos;
        try {
            nanos = delay.get().toNanos();
        } catch (ArithmeticException e) {
            nanos = Long.MAX_VALUE;
        }
        timer.schedule(() -> {
            trigger();
            scheduleNext();
        }, Math.max(1, nanos), TimeUnit.NANOSECONDS);
    }

    void trigger() {
        if (stopped.get() || !running.compareAndSet(false, true)) {
            return;
        }
        worker.execute(() -> {
            try {
                poll();
            } finally {
                running.set(false);
            }
        });
    }

    private void poll() {
        for (Settings.Endpoint endpoint : settings.endpoints()) {
            if (stopped.get() || Thread.currentThread().isInterrupted()) {
                return;
            }
            try {
                String topic = settings.topic() + (endpoint.name().isEmpty() ? "" : "/" + endpoint.name());
                byte[] body = rest.fetch(endpoint);
                List<Mapping.Message> messages = Mapping.map(body, topic, settings.retain(), settings.mapping());
                if (settings.availability() && messages.stream()
                        .anyMatch(message -> message.topic().equals(settings.availabilityTopic()))) {
                    throw new IllegalArgumentException("mapped topic conflicts with mqtt availability topic");
                }
                for (Mapping.Message message : messages) {
                    mqtt.publish(message);
                }
                events.log("debug", "poll completed: " + endpoint.name());
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                return;
            } catch (IOException | MqttException | RuntimeException e) {
                // Transport/library diagnostics may contain URLs, credentials or response fragments.
                if (!stopped.get()) {
                    events.log("error", "poll failed: " + endpoint.name() + " (" + category(e) + ")");
                }
            }
        }
    }

    private static String category(Exception exception) {
        if (exception instanceof IOException) {
            return "http or mapping error";
        }
        if (exception instanceof MqttException) {
            return "mqtt error";
        }
        return "invalid data or pending delivery";
    }

    @Override
    public void close() {
        if (!stopped.compareAndSet(false, true)) {
            return;
        }
        timer.shutdownNow();
        worker.shutdownNow();
        if (rest != null) {
            rest.close();
        }
        try {
            worker.awaitTermination(2, TimeUnit.SECONDS);
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
        }
        if (mqtt != null) {
            mqtt.close();
        }
        if (events != null) {
            events.log("info", "service stopped");
        }
    }
}
