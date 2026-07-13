(function (root, factory) {
  "use strict";

  var api = factory();
  if (typeof module === "object" && module.exports) {
    module.exports = api;
  } else {
    root.GLuaPlaygroundEditorCore = api;
  }
})(typeof globalThis !== "undefined" ? globalThis : this, function () {
  "use strict";

  function leadingWhitespace(line) {
    var match = String(line || "").match(/^\s*/);
    return match ? match[0] : "";
  }

  function alreadyClosed(nextLine, closeText) {
    var trimmed = String(nextLine || "").trim();
    if (!trimmed) return false;
    if (closeText === "end") return /^end\b/.test(trimmed);
    return /^until\b/.test(trimmed);
  }

  function bodyExpansion(indent, closeText) {
    var bodyIndent = indent + "  ";
    return {
      text: "\n" + bodyIndent + "\n" + indent + closeText,
      caretLineDelta: 1,
      caretColumn: bodyIndent.length + 1,
    };
  }

  function opensEndBlock(trimmed) {
    return (/\sdo\s*$/.test(trimmed) && !/^switch\b/.test(trimmed)) ||
      /\sthen\s*$/.test(trimmed) ||
      /^(?:local\s+)?function\b.*\)\s*$/.test(trimmed) ||
      /=\s*function\s*\([^)]*\)\s*$/.test(trimmed) ||
      /\bfunction\s*\([^)]*\)\s*$/.test(trimmed);
  }

  function blockExpansion(lineBeforeCaret, nextLine) {
    var source = String(lineBeforeCaret || "");
    var trimmed = source.trim();
    if (!trimmed) return null;
    var indent = leadingWhitespace(source);

    if (/^switch\b.*\bdo\s*$/.test(trimmed)) {
      if (alreadyClosed(nextLine, "end")) return null;
      var caseIndent = indent + "  ";
      var caseBodyIndent = indent + "    ";
      return {
        text: "\n" + caseIndent + "case \n" + caseBodyIndent + "\n" + indent + "end",
        caretLineDelta: 1,
        caretColumn: caseIndent.length + "case ".length + 1,
      };
    }
    if (/^(?:case\b.+|default)\s*$/.test(trimmed)) {
      var branchIndent = indent + "  ";
      return { text: "\n" + branchIndent, caretLineDelta: 1, caretColumn: branchIndent.length + 1 };
    }
    if (trimmed === "repeat") {
      if (alreadyClosed(nextLine, "until")) return null;
      return bodyExpansion(indent, "until ");
    }
    if (opensEndBlock(trimmed)) {
      if (alreadyClosed(nextLine, "end")) return null;
      return bodyExpansion(indent, "end");
    }
    return null;
  }

  function clampDragDelta(rect, viewport, delta, margin) {
    var safeMargin = Math.max(0, Number(margin) || 0);
    var viewportWidth = Math.max(0, Number(viewport && viewport.width) || 0);
    var viewportHeight = Math.max(0, Number(viewport && viewport.height) || 0);
    var deltaX = Number(delta && delta.x) || 0;
    var deltaY = Number(delta && delta.y) || 0;
    var minimumX = safeMargin - Number(rect && rect.left || 0);
    var maximumX = viewportWidth - safeMargin - Number(rect && rect.right || 0);
    var minimumY = safeMargin - Number(rect && rect.top || 0);
    var maximumY = viewportHeight - safeMargin - Number(rect && rect.bottom || 0);

    if (minimumX > maximumX) deltaX = 0;
    else deltaX = Math.min(maximumX, Math.max(minimumX, deltaX));
    if (minimumY > maximumY) deltaY = 0;
    else deltaY = Math.min(maximumY, Math.max(minimumY, deltaY));

    return { x: deltaX, y: deltaY };
  }

  function completionContext(lineBeforeCursor) {
    var match = String(lineBeforeCursor || "").match(/([A-Za-z_][A-Za-z0-9_]*(?:[.:][A-Za-z_][A-Za-z0-9_]*)*)$/);
    var qualifiedPrefix = match ? match[1] : "";
    var dotIndex = qualifiedPrefix.lastIndexOf(".");
    var colonIndex = qualifiedPrefix.lastIndexOf(":");
    var separatorIndex = Math.max(dotIndex, colonIndex);
    var namespace = separatorIndex >= 0 ? qualifiedPrefix.slice(0, separatorIndex + 1) : "";
    return {
      qualifiedPrefix: qualifiedPrefix,
      namespace: namespace,
      catalogNamespace: namespace.replace(/:/g, "."),
      memberPrefix: separatorIndex >= 0 ? qualifiedPrefix.slice(separatorIndex + 1) : qualifiedPrefix,
    };
  }

  function snippetEscape(value) {
    return String(value || "").replace(/\\/g, "\\\\").replace(/\$/g, "\\$").replace(/}/g, "\\}");
  }

  function completionSnippet(name, signature) {
    var match = String(signature || "").match(/\((.*)\)/);
    if (!match) return snippetEscape(name);
    var parameters = match[1].split(",").map(function (parameter) {
      return parameter.trim().replace(/\[/g, "").replace(/\]/g, "").replace(/\s*=.*$/, "").trim();
    }).filter(Boolean);
    if (parameters.length === 0) return snippetEscape(name) + "()";
    var placeholders = parameters.map(function (parameter, index) {
      return "${" + (index + 1) + ":" + snippetEscape(parameter) + "}";
    });
    return snippetEscape(name) + "(" + placeholders.join(", ") + ")";
  }

  return {
    blockExpansion: blockExpansion,
    clampDragDelta: clampDragDelta,
    completionContext: completionContext,
    completionSnippet: completionSnippet,
  };
});
