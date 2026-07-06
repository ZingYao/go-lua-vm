package com.glua.jetbrains;

import com.intellij.openapi.application.ApplicationManager;
import com.intellij.openapi.options.Configurable;
import org.jetbrains.annotations.Nls;
import org.jetbrains.annotations.Nullable;

import javax.swing.JComponent;
import javax.swing.JLabel;
import javax.swing.JPanel;
import javax.swing.JScrollPane;
import javax.swing.JTextArea;
import javax.swing.JTextField;
import java.awt.BorderLayout;
import java.awt.GridLayout;
import java.util.Arrays;
import java.util.List;

public final class GluaSettingsConfigurable implements Configurable {
    private JTextField docLanguage;
    private JTextArea builtinDocs;
    private JTextField dapHost;
    private JTextField dapPort;
    private JPanel panel;

    @Override
    public @Nls String getDisplayName() {
        return "GLua";
    }

    @Override
    public @Nullable JComponent createComponent() {
        docLanguage = new JTextField();
        builtinDocs = new JTextArea(8, 60);
        dapHost = new JTextField();
        dapPort = new JTextField();
        JPanel fields = new JPanel(new GridLayout(0, 1, 0, 6));
        fields.add(new JLabel("Doc language tag, for example auto, en, zh-CN, ja-JP"));
        fields.add(docLanguage);
        fields.add(new JLabel("Builtin docs JSON files, one absolute or project-relative path per line"));
        fields.add(new JScrollPane(builtinDocs));
        fields.add(new JLabel("DAP attach host, for example 127.0.0.1"));
        fields.add(dapHost);
        fields.add(new JLabel("DAP attach port, 1-65535"));
        fields.add(dapPort);
        panel = new JPanel(new BorderLayout());
        panel.add(fields, BorderLayout.NORTH);
        reset();
        return panel;
    }

    @Override
    public boolean isModified() {
        GluaSettings settings = settings();
        return !settings.docLanguage().equals(docLanguage.getText().trim())
            || !settings.builtinDocs().equals(parseDocs())
            || !settings.dapHost().equals(dapHost.getText().trim())
            || settings.dapPort() != parsePort();
    }

    @Override
    public void apply() {
        GluaSettings settings = settings();
        settings.setDocLanguage(docLanguage.getText());
        settings.setBuiltinDocs(parseDocs());
        settings.setDapHost(dapHost.getText());
        settings.setDapPort(parsePort());
        GluaBuiltinCatalog.getInstance().reload();
    }

    @Override
    public void reset() {
        GluaSettings settings = settings();
        docLanguage.setText(settings.docLanguage());
        builtinDocs.setText(String.join("\n", settings.builtinDocs()));
        dapHost.setText(settings.dapHost());
        dapPort.setText(String.valueOf(settings.dapPort()));
    }

    private List<String> parseDocs() {
        return Arrays.stream(builtinDocs.getText().split("\\R"))
            .map(String::trim)
            .filter(value -> !value.isEmpty())
            .toList();
    }

    private GluaSettings settings() {
        return ApplicationManager.getApplication().getService(GluaSettings.class);
    }

    private int parsePort() {
        try {
            int port = Integer.parseInt(dapPort.getText().trim());
            return port >= 1 && port <= 65535 ? port : 5678;
        } catch (NumberFormatException ignored) {
            return 5678;
        }
    }
}
