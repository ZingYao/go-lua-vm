const assert = require("assert");
const fs = require("fs");
const path = require("path");

const grammarPath = path.join(__dirname, "..", "syntaxes", "glua.tmLanguage.json");
const grammar = JSON.parse(fs.readFileSync(grammarPath, "utf8"));

// 验证顶层确实启用了成员访问规则，避免规则存在但没有被 grammar 引用。
assert(
  grammar.patterns.some((pattern) => pattern.include === "#members"),
  "grammar should include member highlighting"
);

// 验证 glua 与标准库使用相同的命名空间高亮。
const libraryRule = grammar.repository.functions.patterns.find(
  (pattern) => pattern.name === "entity.name.type.library.glua"
);
assert(libraryRule, "library highlighting rule should exist");
assert(new RegExp(libraryRule.match).test("glua.event"), "glua should be highlighted as a namespace");

// 验证方法调用、普通成员和预设事件常量分别获得稳定的 TextMate scope。
const functionMemberRule = grammar.repository.functions.patterns.find(
  (pattern) => pattern.name === "meta.function.member.call.glua"
);
const eventConstantRule = grammar.repository.members.patterns.find(
  (pattern) => pattern.name === "meta.event.constant.access.glua"
);
const propertyRule = grammar.repository.members.patterns.find(
  (pattern) => pattern.name === "meta.member.access.glua"
);
assert(new RegExp(functionMemberRule.match).test(".setProgress("), "member calls should be highlighted");
const eventConstantMatch = new RegExp(eventConstantRule.match).exec(".progress_function_call");
assert(eventConstantMatch && eventConstantMatch.index === 0, "event constants should win from the accessor");
assert.strictEqual(eventConstantMatch[2], "progress_function_call", "event constant capture should be stable");
const propertyMatch = new RegExp(propertyRule.match).exec(".timestamp");
assert(propertyMatch && propertyMatch[2] === "timestamp", "ordinary members should be highlighted");

console.log("syntax highlighting tests passed");
