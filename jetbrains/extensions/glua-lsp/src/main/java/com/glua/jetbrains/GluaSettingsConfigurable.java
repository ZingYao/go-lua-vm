package com.glua.jetbrains;

import com.intellij.openapi.application.ApplicationManager;
import com.intellij.openapi.fileChooser.FileChooser;
import com.intellij.openapi.fileChooser.FileChooserDescriptorFactory;
import com.intellij.openapi.options.Configurable;
import com.intellij.openapi.project.Project;
import com.intellij.openapi.project.ProjectManager;
import com.intellij.openapi.ui.ComboBox;
import com.intellij.openapi.ui.TextFieldWithBrowseButton;
import com.intellij.openapi.vfs.VirtualFile;
import com.intellij.ui.ToolbarDecorator;
import com.intellij.platform.lsp.api.LspServerManager;
import org.jetbrains.annotations.Nls;
import org.jetbrains.annotations.Nullable;

import javax.swing.DefaultListModel;
import javax.swing.JCheckBox;
import javax.swing.JComponent;
import javax.swing.JList;
import javax.swing.JPanel;
import javax.swing.JTextArea;
import javax.swing.JTextField;
import java.awt.BorderLayout;
import java.awt.GridBagConstraints;
import java.awt.GridBagLayout;
import java.awt.Insets;
import java.util.ArrayList;
import java.util.List;

public final class GluaSettingsConfigurable implements Configurable {
    private final ComboBox<String> docLanguage = new ComboBox<>(new String[]{"auto", "en", "zh-CN"});
    private final ComboBox<String> syntax = new ComboBox<>(new String[]{"extended", "lua53", "continue", "switch", "const"});
    private final JCheckBox events = new JCheckBox();
    private final TextFieldWithBrowseButton gluaExecutable = new TextFieldWithBrowseButton();
    private final TextFieldWithBrowseButton gluacExecutable = new TextFieldWithBrowseButton();
    private final TextFieldWithBrowseButton languageServerExecutable = new TextFieldWithBrowseButton();
    private final JCheckBox useRemoteDap = new JCheckBox();
    private final JTextField dapHost = new JTextField();
    private final JTextField dapPort = new JTextField();
    private final JCheckBox dapDebugLog = new JCheckBox();
    private final DefaultListModel<String> builtinDocsModel = new DefaultListModel<>();
    private final JList<String> builtinDocs = new JList<>(builtinDocsModel);
    private JPanel panel;

    @Override
    public @Nls String getDisplayName() {
        return "GLua";
    }

    @Override
    public @Nullable JComponent createComponent() {
        gluaExecutable.addActionListener(ignored -> chooseExecutable(gluaExecutable));
        gluacExecutable.addActionListener(ignored -> chooseExecutable(gluacExecutable));
        languageServerExecutable.addActionListener(ignored -> chooseExecutable(languageServerExecutable));
        useRemoteDap.addActionListener(ignored -> updateDapModeFields());
        builtinDocs.setVisibleRowCount(4);

        JPanel docsPanel = ToolbarDecorator.createDecorator(builtinDocs)
            .setAddAction(ignored -> chooseBuiltinDoc())
            .setRemoveAction(ignored -> {
                int index = builtinDocs.getSelectedIndex();
                if (index >= 0) {
                    builtinDocsModel.remove(index);
                }
            })
            .disableUpDownActions()
            .createPanel();

        panel = new JPanel(new GridBagLayout());
        addRow(0, GluaUiText.text("Documentation language", "文档语言"), docLanguage);
        addRow(1, GluaUiText.text("Syntax mode", "语法模式"), syntax);
        addRow(2, GluaUiText.text("GLua events", "GLua events"), events);
        addRow(3, GluaUiText.text("glua executable", "glua 可执行文件"), gluaExecutable);
        addRow(4, GluaUiText.text("gluac executable", "gluac 可执行文件"), gluacExecutable);
        addRow(5, GluaUiText.text("gluals executable", "gluals 可执行文件"), languageServerExecutable);
        addRow(6, GluaUiText.text("Use remote DAP", "使用远程 DAP"), useRemoteDap);
        addRow(7, GluaUiText.text("Remote DAP host", "远程 DAP 主机"), dapHost);
        addRow(8, GluaUiText.text("Remote DAP port", "远程 DAP 端口"), dapPort);
        addRow(9, GluaUiText.text("Print DAP traffic", "输出 DAP 调试日志"), dapDebugLog);
        addRow(10, GluaUiText.text("Builtin docs JSON files", "内置文档 JSON 文件"), docsPanel);
        addRow(11, GluaUiText.text("Builtin docs JSON demo", "内置文档 JSON 示例"), demoText());
        reset();
        return panel;
    }

    @Override
    public boolean isModified() {
        GluaSettings settings = settings();
        return !settings.docLanguage().equals(String.valueOf(docLanguage.getSelectedItem()))
            || !settings.syntax().equals(String.valueOf(syntax.getSelectedItem()))
            || settings.events() != events.isSelected()
            || !settings.gluaExecutable().equals(gluaExecutable.getText().trim())
            || !settings.gluacExecutable().equals(gluacExecutable.getText().trim())
            || !settings.languageServerExecutable().equals(languageServerExecutable.getText().trim())
            || settings.useRemoteDap() != useRemoteDap.isSelected()
            || !settings.dapHost().equals(dapHost.getText().trim())
            || settings.dapPort() != parsePortOrDefault(dapPort.getText())
            || settings.dapDebugLog() != dapDebugLog.isSelected()
            || !settings.builtinDocs().equals(docs());
    }

    @Override
    public void apply() {
        GluaSettings settings = settings();
        settings.setDocLanguage(String.valueOf(docLanguage.getSelectedItem()));
        settings.setSyntax(String.valueOf(syntax.getSelectedItem()));
        settings.setEvents(events.isSelected());
        settings.setGluaExecutable(gluaExecutable.getText());
        settings.setGluacExecutable(gluacExecutable.getText());
        settings.setLanguageServerExecutable(languageServerExecutable.getText());
        settings.setUseRemoteDap(useRemoteDap.isSelected());
        settings.setDapHost(dapHost.getText());
        settings.setDapPort(parsePortOrDefault(dapPort.getText()));
        settings.setDapDebugLog(dapDebugLog.isSelected());
        settings.setBuiltinDocs(docs());
        GluaBuiltinCatalog.getInstance().reload();
        for (Project project : ProjectManager.getInstance().getOpenProjects()) {
            LspServerManager.getInstance(project).stopAndRestartIfNeeded(GluaLspServerSupportProvider.class);
        }
    }

    @Override
    public void reset() {
        GluaSettings settings = settings();
        docLanguage.setSelectedItem(settings.docLanguage());
        syntax.setSelectedItem(settings.syntax());
        events.setSelected(settings.events());
        gluaExecutable.setText(settings.gluaExecutable());
        gluacExecutable.setText(settings.gluacExecutable());
        languageServerExecutable.setText(settings.languageServerExecutable());
        useRemoteDap.setSelected(settings.useRemoteDap());
        dapHost.setText(settings.dapHost());
        dapPort.setText(String.valueOf(settings.dapPort()));
        dapDebugLog.setSelected(settings.dapDebugLog());
        builtinDocsModel.clear();
        for (String doc : settings.builtinDocs()) {
            builtinDocsModel.addElement(doc);
        }
        updateDapModeFields();
    }

    private void updateDapModeFields() {
        boolean remote = useRemoteDap.isSelected();
        gluaExecutable.setEnabled(!remote);
        dapHost.setEnabled(remote);
        dapPort.setEnabled(remote);
    }

    private void addRow(int row, String label, JComponent component) {
        GridBagConstraints labelConstraints = new GridBagConstraints();
        labelConstraints.gridx = 0;
        labelConstraints.gridy = row;
        labelConstraints.insets = new Insets(6, 0, 6, 12);
        labelConstraints.anchor = GridBagConstraints.WEST;
        panel.add(new javax.swing.JLabel(label), labelConstraints);

        GridBagConstraints fieldConstraints = new GridBagConstraints();
        fieldConstraints.gridx = 1;
        fieldConstraints.gridy = row;
        fieldConstraints.weightx = 1.0;
        fieldConstraints.fill = GridBagConstraints.HORIZONTAL;
        fieldConstraints.insets = new Insets(6, 0, 6, 0);
        if (component.getPreferredSize().height > 60) {
            fieldConstraints.fill = GridBagConstraints.BOTH;
            fieldConstraints.weighty = 1.0;
        }
        panel.add(component, fieldConstraints);
    }

    private void chooseBuiltinDoc() {
        VirtualFile file = FileChooser.chooseFile(
            FileChooserDescriptorFactory.createSingleFileDescriptor("json"),
            null,
            null
        );
        if (file != null) {
            builtinDocsModel.addElement(file.getPath());
        }
    }

    private void chooseExecutable(TextFieldWithBrowseButton field) {
        VirtualFile file = FileChooser.chooseFile(
            FileChooserDescriptorFactory.createSingleFileNoJarsDescriptor(),
            null,
            null
        );
        if (file != null) {
            field.setText(file.getPath());
        }
    }

    private List<String> docs() {
        List<String> result = new ArrayList<>();
        for (int index = 0; index < builtinDocsModel.size(); index++) {
            String value = builtinDocsModel.get(index);
            if (value != null && !value.isBlank()) {
                result.add(value.trim());
            }
        }
        return result;
    }

    private GluaSettings settings() {
        return ApplicationManager.getApplication().getService(GluaSettings.class);
    }

    private static int parsePortOrDefault(String value) {
        try {
            int port = Integer.parseInt(value == null ? "" : value.trim());
            return port >= 1 && port <= 65535 ? port : GluaDapRunConfiguration.INTERNAL_DAP_PORT;
        } catch (NumberFormatException ignored) {
            return GluaDapRunConfiguration.INTERNAL_DAP_PORT;
        }
    }

    private static JTextArea demoText() {
        JTextArea area = new JTextArea("""
            {
              "functions": {
                "module.timesPrint": {
                  "signature": {
                    "en": "module.timesPrint(name, times)",
                    "zh-CN": "module.timesPrint(name, times)"
                  },
                  "description": {
                    "en": "Prints name repeatedly.",
                    "zh-CN": "重复打印名称。"
                  },
                  "params": {
                    "en": ["name: text to print", "times: repeat count"],
                    "zh-CN": ["name: 要打印的文本", "times: 重复次数"]
                  },
                  "returns": {
                    "en": "returns: nil",
                    "zh-CN": "返回：nil"
                  }
                }
              }
            }""");
        area.setEditable(false);
        area.setRows(12);
        area.setLineWrap(false);
        return area;
    }
}
