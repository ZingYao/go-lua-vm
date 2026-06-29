package com.glua.jetbrains;

import com.intellij.openapi.fileTypes.LanguageFileType;
import org.jetbrains.annotations.Nls;
import org.jetbrains.annotations.NonNls;
import org.jetbrains.annotations.NotNull;
import org.jetbrains.annotations.Nullable;

import javax.swing.Icon;

public final class GluaFileType extends LanguageFileType {
    public static final GluaFileType INSTANCE = new GluaFileType();

    private GluaFileType() {
        super(GluaLanguage.INSTANCE);
    }

    @Override
    public @NonNls @NotNull String getName() {
        return "GLua";
    }

    @Override
    public @Nls @NotNull String getDescription() {
        return "glua source file";
    }

    @Override
    public @NotNull String getDefaultExtension() {
        return "glua";
    }

    @Override
    public @Nullable Icon getIcon() {
        return null;
    }
}
