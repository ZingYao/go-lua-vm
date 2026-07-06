package com.glua.jetbrains;

import com.intellij.openapi.actionSystem.AnAction;
import com.intellij.openapi.actionSystem.AnActionEvent;
import com.intellij.openapi.application.ApplicationManager;
import com.intellij.openapi.ide.CopyPasteManager;
import com.intellij.openapi.ui.Messages;
import org.jetbrains.annotations.NotNull;

import java.awt.datatransfer.StringSelection;

public final class GluaDapAttachConfigAction extends AnAction {
    @Override
    public void actionPerformed(@NotNull AnActionEvent event) {
        GluaSettings settings = ApplicationManager.getApplication().getService(GluaSettings.class);
        String payload = String.format("""
            {
              "type": "glua",
              "request": "attach",
              "name": "Attach to GLua DAP",
              "host": "%s",
              "port": %d
            }
            """, settings.dapHost(), settings.dapPort());
        CopyPasteManager.getInstance().setContents(new StringSelection(payload));
        Messages.showInfoMessage(
            event.getProject(),
            "GLua DAP attach configuration copied to clipboard.",
            "GLua DAP"
        );
    }
}
