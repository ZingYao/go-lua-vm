package com.glua.jetbrains;

import com.intellij.openapi.fileChooser.FileChooser;
import com.intellij.openapi.fileChooser.FileChooserDescriptor;
import com.intellij.openapi.fileChooser.FileChooserDescriptorFactory;
import com.intellij.openapi.options.ConfigurationException;
import com.intellij.openapi.options.SettingsEditor;
import com.intellij.openapi.ui.TextFieldWithBrowseButton;
import com.intellij.openapi.vfs.VirtualFile;
import org.jetbrains.annotations.NotNull;

import javax.swing.JCheckBox;
import javax.swing.JComponent;
import javax.swing.JLabel;
import javax.swing.JPanel;
import javax.swing.JTextField;
import java.awt.GridBagConstraints;
import java.awt.GridBagLayout;
import java.awt.Insets;

public final class GluaDapRunConfigurationEditor extends SettingsEditor<GluaDapRunConfiguration> {
    private final TextFieldWithBrowseButton gluaExecutable = new TextFieldWithBrowseButton();
    private final TextFieldWithBrowseButton program = new TextFieldWithBrowseButton();
    private final JCheckBox useRemoteDap = new JCheckBox();
    private final JTextField dapHost = new JTextField();
    private final JTextField dapPort = new JTextField();

    @Override
    protected void resetEditorFrom(@NotNull GluaDapRunConfiguration configuration) {
        gluaExecutable.setText(configuration.gluaExecutable());
        program.setText(configuration.program());
        useRemoteDap.setSelected(configuration.useRemoteDap());
        dapHost.setText(configuration.host());
        dapPort.setText(String.valueOf(configuration.port()));
        updateDapModeFields();
    }

    @Override
    protected void applyEditorTo(@NotNull GluaDapRunConfiguration configuration) throws ConfigurationException {
        String nextProgram = program.getText() == null ? "" : program.getText().trim();
        if (nextProgram.isBlank()) {
            throw new ConfigurationException(GluaUiText.text("GLua program file is required.", "必须选择 GLua 程序文件。"));
        }
        configuration.setGluaExecutable(gluaExecutable.getText());
        configuration.setProgram(nextProgram);
        configuration.setUseRemoteDap(useRemoteDap.isSelected());
        configuration.setDapHost(dapHost.getText());
        configuration.setDapPort(parsePort(dapPort.getText()));
    }

    @Override
    protected @NotNull JComponent createEditor() {
        gluaExecutable.addActionListener(ignored -> chooseExecutable(gluaExecutable, GluaUiText.text("Select glua executable", "选择 glua 可执行文件")));
        program.addActionListener(ignored -> chooseExecutable(program, GluaUiText.text("Select GLua/Lua file", "选择 GLua/Lua 文件")));
        useRemoteDap.addActionListener(ignored -> updateDapModeFields());
        JPanel panel = new JPanel(new GridBagLayout());
        addRow(panel, 0, GluaUiText.text("glua executable", "glua 可执行文件"), gluaExecutable);
        addRow(panel, 1, GluaUiText.text("Program", "程序文件"), program);
        addRow(panel, 2, GluaUiText.text("Use remote DAP", "使用远程 DAP"), useRemoteDap);
        addRow(panel, 3, GluaUiText.text("Remote DAP host", "远程 DAP 主机"), dapHost);
        addRow(panel, 4, GluaUiText.text("Remote DAP port", "远程 DAP 端口"), dapPort);
        updateDapModeFields();
        return panel;
    }

    private void updateDapModeFields() {
        boolean remote = useRemoteDap.isSelected();
        gluaExecutable.setEnabled(!remote);
        dapHost.setEnabled(remote);
        dapPort.setEnabled(remote);
    }

    private static void addRow(JPanel panel, int row, String label, JComponent component) {
        GridBagConstraints labelConstraints = new GridBagConstraints();
        labelConstraints.gridx = 0;
        labelConstraints.gridy = row;
        labelConstraints.insets = new Insets(6, 0, 6, 12);
        labelConstraints.anchor = GridBagConstraints.WEST;
        panel.add(new JLabel(label), labelConstraints);

        GridBagConstraints fieldConstraints = new GridBagConstraints();
        fieldConstraints.gridx = 1;
        fieldConstraints.gridy = row;
        fieldConstraints.weightx = 1.0;
        fieldConstraints.fill = GridBagConstraints.HORIZONTAL;
        fieldConstraints.insets = new Insets(6, 0, 6, 0);
        panel.add(component, fieldConstraints);
    }

    private void chooseExecutable(TextFieldWithBrowseButton field, String title) {
        FileChooserDescriptor descriptor = FileChooserDescriptorFactory.createSingleFileNoJarsDescriptor()
            .withTitle(title);
        VirtualFile file = FileChooser.chooseFile(
            descriptor,
            null,
            null
        );
        if (file != null) {
            field.setText(file.getPath());
        }
    }

    private static int parsePort(String value) throws ConfigurationException {
        try {
            int port = Integer.parseInt(value == null ? "" : value.trim());
            if (port >= 1 && port <= 65535) {
                return port;
            }
        } catch (NumberFormatException ignored) {
            // 端口非法时统一走下面的配置错误提示。
        }
        throw new ConfigurationException(GluaUiText.text("Remote DAP port must be between 1 and 65535.", "远程 DAP 端口必须在 1 到 65535 之间。"));
    }
}
