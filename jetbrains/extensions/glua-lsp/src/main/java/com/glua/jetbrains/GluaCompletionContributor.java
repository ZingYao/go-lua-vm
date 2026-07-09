package com.glua.jetbrains;

import com.intellij.codeInsight.completion.CompletionContributor;
import com.intellij.codeInsight.completion.CompletionParameters;
import com.intellij.codeInsight.completion.CompletionProvider;
import com.intellij.codeInsight.completion.CompletionResultSet;
import com.intellij.codeInsight.completion.CompletionType;
import com.intellij.codeInsight.completion.InsertHandler;
import com.intellij.codeInsight.lookup.LookupElement;
import com.intellij.codeInsight.lookup.LookupElementBuilder;
import com.intellij.codeInsight.template.Template;
import com.intellij.codeInsight.template.TemplateManager;
import com.intellij.openapi.editor.Document;
import com.intellij.patterns.PlatformPatterns;
import com.intellij.util.ProcessingContext;
import org.jetbrains.annotations.NotNull;

import java.util.ArrayList;
import java.util.List;

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
                if (completion.keywordOnly()) {
                    if ("do".startsWith(completion.prefix())) {
                        result.addElement(LookupElementBuilder.create("do")
                            .withTypeText("Lua keyword", true));
                    }
                    return;
                }
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
                            .withTailText(" " + builtin.description, true)
                            .withInsertHandler(insertFunctionTemplate(method, builtin.signature)));
                        continue;
                    }
                    if (name.contains(".") || !name.startsWith(completion.prefix())) {
                        continue;
                    }
                    result.addElement(LookupElementBuilder.create(name)
                        .withTypeText(builtin.signature, true)
                        .withTailText(" " + builtin.description, true)
                        .withInsertHandler(insertFunctionTemplate(name, builtin.signature)));
                }
                if (!completion.method()) {
                    for (GluaAnalysis.SymbolCompletion symbol : GluaAnalysis.symbolCompletions(parameters.getEditor().getDocument(), completion.prefix())) {
                        LookupElementBuilder builder = LookupElementBuilder.create(symbol.name())
                            .withTypeText(symbol.signature() == null ? "GLua file symbol" : symbol.signature(), true)
                            .withTailText(symbol.signature() == null ? " declared in current file" : " function in current file", true);
                        if (symbol.signature() != null) {
                            builder = builder.withInsertHandler(insertFunctionTemplate(symbol.name(), symbol.signature()));
                        }
                        result.addElement(builder);
                    }
                } else {
                    for (GluaRequireSupport.ExportedMember member : GluaRequireSupport.requiredModuleCompletionMembers(
                        parameters.getOriginalFile(),
                        completion.receiver(),
                        completion.separator(),
                        completion.prefix()
                    )) {
                        String typeText = member.detail();
                        String tailText = " from " + member.sourcePath().getFileName();
                        result.addElement(LookupElementBuilder.create(member.name())
                            .withTypeText(typeText, true)
                            .withTailText(tailText, true)
                            .withInsertHandler(insertFunctionTemplate(member.name(), member.signature())));
                    }
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

    private static InsertHandler<LookupElement> insertFunctionTemplate(String name, String signature) {
        return (context, item) -> {
            Document document = context.getDocument();
            int start = context.getStartOffset();
            int end = context.getTailOffset();
            document.deleteString(start, end);
            context.getEditor().getCaretModel().moveToOffset(start);
            Template template = TemplateManager.getInstance(context.getProject()).createTemplate("", "GLua");
            template.addTextSegment(name + "(");
            List<String> params = signatureParameters(signature);
            for (int i = 0; i < params.size(); i++) {
                if (i > 0) {
                    template.addTextSegment(", ");
                }
                String variableName = "p" + i;
                String defaultValue = quoteTemplateExpression(params.get(i));
                template.addVariable(variableName, defaultValue, defaultValue, true);
                template.addVariableSegment(variableName);
            }
            template.addTextSegment(")");
            template.addEndVariable();
            template.setToReformat(false);
            TemplateManager.getInstance(context.getProject()).startTemplate(context.getEditor(), template);
        };
    }

    static String functionSnippetText(String name, String signature) {
        List<String> params = signatureParameters(signature);
        return name + "(" + String.join(", ", params) + ")";
    }

    static String quoteTemplateExpression(String value) {
        return "\"" + value.replace("\\", "\\\\").replace("\"", "\\\"") + "\"";
    }

    private static List<String> signatureParameters(String signature) {
        java.util.regex.Matcher matcher = java.util.regex.Pattern.compile("\\((.*)\\)").matcher(signature == null ? "" : signature);
        if (!matcher.find()) {
            return List.of();
        }
        List<String> params = new ArrayList<>();
        for (String raw : matcher.group(1).split(",")) {
            String param = cleanupSignatureParameter(raw);
            if (!param.isBlank()) {
                params.add(param);
            }
        }
        return params;
    }

    private static String cleanupSignatureParameter(String raw) {
        return raw.trim()
            .replace("[", "")
            .replace("]", "")
            .replaceAll("\\s*=.*$", "")
            .trim();
    }
}
