package de.buderus.bridge;

import io.smallrye.config.ConfigMapping;
import java.util.Optional;

@ConfigMapping(prefix = "bridge")
public interface BridgeConfig {
    // Raw values keep validation errors free of credentials and preserve Go-compatible syntax.
    Optional<String> apiTimeout();
    Optional<String> apiMaxBodyBytes();
    Optional<String> apiInsecureSkipVerify();
    Optional<String> mqttBroker();
    Optional<String> mqttClientId();
    Optional<String> mqttTopic();
    Optional<String> mqttQos();
    Optional<String> mqttRetain();
    Optional<String> mqttPublishTimeout();
    Optional<String> mqttAvailability();
    Optional<String> mapping();
    Optional<String> schedule();
    Optional<String> pollOnStart();
    Optional<String> timezone();
    Optional<String> logLevel();
    Optional<String> logFormat();
}
