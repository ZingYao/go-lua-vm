package com.glua.jetbrains;

import com.intellij.codeInsight.completion.CompletionContributor;
import com.intellij.codeInsight.completion.CompletionParameters;
import com.intellij.codeInsight.completion.CompletionProvider;
import com.intellij.codeInsight.completion.CompletionResultSet;
import com.intellij.codeInsight.completion.CompletionType;
import com.intellij.codeInsight.completion.InsertHandler;
import com.intellij.codeInsight.lookup.LookupElement;
import com.intellij.codeInsight.lookup.LookupElementBuilder;
import com.intellij.openapi.editor.Document;
import com.intellij.patterns.PlatformPatterns;
import com.intellij.util.ProcessingContext;
import org.jetbrains.annotations.NotNull;

public final class GluaCompletionContributor extends CompletionContributor {
    public GluaCompletionContributor() {
        extend(CompletionType.BASIC, PlatformPatterns.psiElement(), new CompletionProvider<>() {
            @Override
            protected void addCompletions(@NotNull CompletionParameters parameters,
                                          @NotNull ProcessingContext context,
                                          @NotNull CompletionResultSet result) {
                if (parameters.getOriginalFile().getFileType() != GluaFileType.INSTANCE) {
                    return;
                }
                GluaBuiltinCatalog catalog = GluaBuiltinCatalog.getInstance();
                GluaAnalysis.CompletionContext completion = GluaAnalysis.completionContext(
                    parameters.getEditor().getDocument(),
                    parameters.getOffset()
                );
                addAnnotationTemplates(completion, result);
                for (String name : catalog.sortedNames()) {
                    GluaBuiltin builtin = catalog.get(name);
                    if (builtin == null) {
                        continue;
                    }
                    if (completion.method()) {
                        String prefix = completion.module() + ".";
                        if (!name.startsWith(prefix)) {
                            continue;
                        }
                        String method = name.substring(prefix.length());
                        if (!method.startsWith(completion.prefix())) {
                            continue;
                        }
                        result.addElement(LookupElementBuilder.create(method)
                            .withTypeText(builtin.signature, true)
                            .withTailText(" " + builtin.description, true));
                        continue;
                    }
                    if (name.contains(".") || !name.startsWith(completion.prefix())) {
                        continue;
                    }
                    result.addElement(LookupElementBuilder.create(name)
                        .withTypeText(builtin.signature, true)
                        .withTailText(" " + builtin.description, true));
                }
            }
        });
    }

    private static void addAnnotationTemplates(GluaAnalysis.CompletionContext completion, CompletionResultSet result) {
        if (completion.method()) {
            return;
        }
        String prefix = completion.prefix().toLowerCase();
        if (!prefix.isBlank() && !"doc".startsWith(prefix) && !"docs".startsWith(prefix) && !"func".startsWith(prefix) && !"function".startsWith(prefix) && !"glua".startsWith(prefix)) {
            return;
        }
        result.addElement(LookupElementBuilder.create("glua doc comment")
            .withTypeText("GLua annotation", true)
            .withTailText(" standard parseable comment", true)
            .withInsertHandler(insertText(GluaUserDocumentation.standardSnippet())));
        result.addElement(LookupElementBuilder.create("glua documented function")
            .withTypeText("GLua annotation + function", true)
            .withTailText(" table method template", true)
            .withInsertHandler(insertText(String.join("\n",
                GluaUserDocumentation.standardSnippet(),
                "module.functionName = function(name)",
                "  ",
                "end"
            ))));
    }

    private static InsertHandler<LookupElement> insertText(String text) {
        return (context, item) -> {
            Document document = context.getDocument();
            int start = context.getStartOffset();
            int end = context.getTailOffset();
            document.replaceString(start, end, text);
            context.getEditor().getCaretModel().moveToOffset(start + text.length());
        };
    }
}
