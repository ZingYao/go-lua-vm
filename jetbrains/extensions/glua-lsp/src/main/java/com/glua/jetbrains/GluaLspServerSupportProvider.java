package com.glua.jetbrains;

import com.intellij.openapi.application.ApplicationManager;
import com.intellij.openapi.project.Project;
import com.intellij.openapi.ui.Messages;
import com.intellij.openapi.vfs.VirtualFile;
import com.intellij.platform.lsp.api.LspServerSupportProvider;
import org.jetbrains.annotations.NotNull;

import java.io.IOException;
import java.util.concurrent.atomic.AtomicBoolean;

public final class GluaLspServerSupportProvider implements LspServerSupportProvider {
    private static final AtomicBoolean PATH_ERROR_SHOWN = new AtomicBoolean();

    @Override
    public void fileOpened(@NotNull Project project, @NotNull VirtualFile file, @NotNull LspServerStarter serverStarter) {
        String extension = file.getExtension();
        if (extension == null || (!extension.equalsIgnoreCase("lua") && !extension.equalsIgnoreCase("glua"))) {
            return;
        }

        GluaSettings settings = ApplicationManager.getApplication().getService(GluaSettings.class);
        try {
            GluaLanguageServerExecutable.resolve(settings.languageServerExecutable());
            serverStarter.ensureServerStarted(new GluaLspServerDescriptor(project));
        } catch (IOException error) {
            if (PATH_ERROR_SHOWN.compareAndSet(false, true)) {
                ApplicationManager.getApplication().invokeLater(() -> Messages.showErrorDialog(project, error.getMessage(), "GLua Language Server"));
            }
        }
    }
}
