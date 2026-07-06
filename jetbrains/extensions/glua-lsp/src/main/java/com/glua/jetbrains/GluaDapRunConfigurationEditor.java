package com.glua.jetbrains;

import com.intellij.openapi.options.ConfigurationException;
import com.intellij.openapi.options.SettingsEditor;
import org.jetbrains.annotations.NotNull;

import javax.swing.JComponent;
import javax.swing.JLabel;
import javax.swing.JPanel;
import javax.swing.JTextField;
import java.awt.GridLayout;

public final class GluaDapRunConfigurationEditor extends SettingsEditor<GluaDapRunConfiguration> {
    private final JTextField host = new JTextField();
    private final JTextField port = new JTextField();

    @Override
    protected void resetEditorFrom(@NotNull GluaDapRunConfiguration configuration) {
        host.setText(configuration.host());
        port.setText(String.valueOf(configuration.port()));
    }

    @Override
    protected void applyEditorTo(@NotNull GluaDapRunConfiguration configuration) throws ConfigurationException {
        String nextHost = host.getText() == null ? "" : host.getText().trim();
        if (nextHost.isBlank()) {
            throw new ConfigurationException("DAP attach host is required.");
        }
        configuration.setHost(nextHost);
        configuration.setPort(parsePort());
    }

    @Override
    protected @NotNull JComponent createEditor() {
        JPanel panel = new JPanel(new GridLayout(0, 1, 0, 6));
        panel.add(new JLabel("DAP attach host, for example 127.0.0.1"));
        panel.add(host);
        panel.add(new JLabel("DAP attach port, 1-65535"));
        panel.add(port);
        return panel;
    }

    private int parsePort() throws ConfigurationException {
        try {
            int value = Integer.parseInt(port.getText().trim());
            if (value < 1 || value > 65535) {
                throw new ConfigurationException("DAP attach port must be between 1 and 65535.");
            }
            return value;
        } catch (NumberFormatException ignored) {
            throw new ConfigurationException("DAP attach port must be a number.");
        }
    }
}
