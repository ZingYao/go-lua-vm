package com.glua.jetbrains;

import com.intellij.execution.Executor;
import com.intellij.execution.RunManager;
import com.intellij.execution.RunnerAndConfigurationSettings;
import com.intellij.execution.ProgramRunnerUtil;
import com.intellij.execution.configurations.ConfigurationType;
import com.intellij.execution.executors.DefaultDebugExecutor;
import com.intellij.execution.executors.DefaultRunExecutor;
import com.intellij.openapi.actionSystem.AnAction;
import com.intellij.openapi.actionSystem.AnActionEvent;
import com.intellij.openapi.actionSystem.CommonDataKeys;
import com.intellij.openapi.application.ApplicationManager;
import com.intellij.openapi.fileEditor.FileDocumentManager;
import com.intellij.openapi.project.Project;
import com.intellij.openapi.ui.Messages;
import com.intellij.openapi.util.io.FileUtil;
import com.intellij.openapi.vfs.VirtualFile;
import org.jetbrains.annotations.NotNull;

import java.nio.file.Path;

public final class GluaRunCurrentFileAction extends AnAction {
    private final boolean debug;

    public GluaRunCurrentFileAction() {
        this(false);
    }

    private GluaRunCurrentFileAction(boolean debug) {
        this.debug = debug;
    }

    @Override
    public void actionPerformed(@NotNull AnActionEvent event) {
        Project project = event.getProject();
        VirtualFile file = gluaFile(event);
        if (project == null || file == null) {
            Messages.showWarningDialog(project, GluaUiText.text("Open a .glua or .lua file first.", "请先打开 .glua 或 .lua 文件。"), "GLua");
            return;
        }
        FileDocumentManager.getInstance().saveAllDocuments();
        executeFile(project, file, debug ? DefaultDebugExecutor.getDebugExecutorInstance() : DefaultRunExecutor.getRunExecutorInstance());
    }

    @Override
    public void update(@NotNull AnActionEvent event) {
        event.getPresentation().setEnabledAndVisible(gluaFile(event) != null);
    }

    private static VirtualFile gluaFile(AnActionEvent event) {
        VirtualFile file = event.getData(CommonDataKeys.VIRTUAL_FILE);
        if (file == null || file.isDirectory()) {
            return null;
        }
        String path = file.getPath();
        return path.endsWith(".glua") || path.endsWith(".lua") ? file : null;
    }

    private static void executeFile(Project project, VirtualFile file, Executor executor) {
        GluaSettings settings = ApplicationManager.getApplication().getService(GluaSettings.class);
        GluaDapRunConfigurationType type = dapConfigurationType();
        if (type == null) {
            Messages.showErrorDialog(project, GluaUiText.text(
                "GLua run configuration is not registered.",
                "GLua 运行配置未注册。"
            ), "GLua");
            return;
        }
        GluaDapRunConfigurationFactory factory = (GluaDapRunConfigurationFactory) type.getConfigurationFactories()[0];
        String prefix = DefaultDebugExecutor.EXECUTOR_ID.equals(executor.getId()) ? "Debug " : "Run ";
        GluaDapRunConfiguration configuration = new GluaDapRunConfiguration(project, factory, prefix + file.getName());
        configuration.setGluaExecutable(settings.gluaExecutable());
        configuration.setUseRemoteDap(settings.useRemoteDap());
        configuration.setDapHost(settings.dapHost());
        configuration.setDapPort(settings.dapPort());
        configuration.setProgram(FileUtil.toSystemIndependentName(Path.of(file.getPath()).toString()));
        configuration.setAllowRunningInParallel(true);
        RunnerAndConfigurationSettings runSettings = RunManager.getInstance(project).createConfiguration(configuration, factory);
        RunManager.getInstance(project).addConfiguration(runSettings);
        RunManager.getInstance(project).setSelectedConfiguration(runSettings);
        ProgramRunnerUtil.executeConfiguration(runSettings, executor);
    }

    private static GluaDapRunConfigurationType dapConfigurationType() {
        for (ConfigurationType type : ConfigurationType.CONFIGURATION_TYPE_EP.getExtensionList()) {
            if (type instanceof GluaDapRunConfigurationType gluaType) {
                return gluaType;
            }
        }
        return null;
    }

    public static final class Debug extends AnAction {
        @Override
        public void actionPerformed(@NotNull AnActionEvent event) {
            new GluaRunCurrentFileAction(true).actionPerformed(event);
        }

        @Override
        public void update(@NotNull AnActionEvent event) {
            new GluaRunCurrentFileAction(true).update(event);
        }
    }
}
