package com.glua.jetbrains;

import com.intellij.openapi.application.ApplicationManager;
import com.intellij.openapi.fileChooser.FileChooser;
import com.intellij.openapi.fileChooser.FileChooserDescriptorFactory;
import com.intellij.openapi.options.Configurable;
import com.intellij.openapi.ui.ComboBox;
import com.intellij.openapi.ui.TextFieldWithBrowseButton;
import com.intellij.openapi.vfs.VirtualFile;
import com.intellij.ui.ToolbarDecorator;
import org.jetbrains.annotations.Nls;
import org.jetbrains.annotations.Nullable;

import javax.swing.DefaultListModel;
import javax.swing.JComponent;
import javax.swing.JList;
import javax.swing.JPanel;
import javax.swing.JTextArea;
import java.awt.BorderLayout;
import java.awt.GridBagConstraints;
import java.awt.GridBagLayout;
import java.awt.Insets;
import java.util.ArrayList;
import java.util.List;

public final class GluaSettingsConfigurable implements Configurable {
    private final ComboBox<String> docLanguage = new ComboBox<>(new String[]{"auto", "en", "zh-CN"});
    private final TextFieldWithBrowseButton gluaExecutable = new TextFieldWithBrowseButton();
    private final TextFieldWithBrowseButton gluacExecutable = new TextFieldWithBrowseButton();
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
        addRow(1, GluaUiText.text("glua executable", "glua 可执行文件"), gluaExecutable);
        addRow(2, GluaUiText.text("gluac executable", "gluac 可执行文件"), gluacExecutable);
        addRow(3, GluaUiText.text("Builtin docs JSON files", "内置文档 JSON 文件"), docsPanel);
        addRow(4, GluaUiText.text("Builtin docs JSON demo", "内置文档 JSON 示例"), demoText());
        reset();
        return panel;
    }

    @Override
    public boolean isModified() {
        GluaSettings settings = settings();
        return !settings.docLanguage().equals(String.valueOf(docLanguage.getSelectedItem()))
            || !settings.gluaExecutable().equals(gluaExecutable.getText().trim())
            || !settings.gluacExecutable().equals(gluacExecutable.getText().trim())
            || !settings.builtinDocs().equals(docs());
    }

    @Override
    public void apply() {
        GluaSettings settings = settings();
        settings.setDocLanguage(String.valueOf(docLanguage.getSelectedItem()));
        settings.setGluaExecutable(gluaExecutable.getText());
        settings.setGluacExecutable(gluacExecutable.getText());
        settings.setBuiltinDocs(docs());
        GluaBuiltinCatalog.getInstance().reload();
    }

    @Override
    public void reset() {
        GluaSettings settings = settings();
        docLanguage.setSelectedItem(settings.docLanguage());
        gluaExecutable.setText(settings.gluaExecutable());
        gluacExecutable.setText(settings.gluacExecutable());
        builtinDocsModel.clear();
        for (String doc : settings.builtinDocs()) {
            builtinDocsModel.addElement(doc);
        }
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
