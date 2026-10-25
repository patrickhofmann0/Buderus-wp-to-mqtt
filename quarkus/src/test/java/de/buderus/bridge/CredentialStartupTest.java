package de.buderus.bridge;

import static org.junit.jupiter.api.Assertions.*;

import io.quarkus.test.junit.QuarkusTestProfile;
import io.quarkus.test.junit.TestProfile;
import io.quarkus.test.junit.main.LaunchResult;
import io.quarkus.test.junit.main.QuarkusMainLauncher;
import io.quarkus.test.junit.main.QuarkusMainTest;
import java.util.Map;
import org.junit.jupiter.api.Test;

@QuarkusMainTest
@TestProfile(CredentialStartupTest.InvalidCredentials.class)
class CredentialStartupTest {
    public static class InvalidCredentials implements QuarkusTestProfile {
        @Override
        public Map<String, String> getConfigOverrides() {
            return Map.of("API_URL", "http://localhost/startup-test", "API_USER", "placeholder",
                    "API_PASSWORD", "prefix${private-fragment}suffix");
        }
    }

    @Test
    void unresolvedCredentialExpressionCannotLeakThroughBootstrapDiagnostics(QuarkusMainLauncher launcher) {
        LaunchResult result = launcher.launch();
        assertEquals(1, result.exitCode());
        String output = result.getOutput() + result.getErrorOutput();
        assertTrue(output.contains("configuration rejected"));
        assertFalse(output.contains("private-fragment"));
        assertFalse(output.contains("prefix"));
        assertFalse(output.contains("suffix"));
        assertFalse(output.contains("SRCFG00011"));
    }
}
