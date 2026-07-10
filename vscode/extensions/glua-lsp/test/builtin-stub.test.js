const assert = require("assert");
const { getBuiltinFunction, makeBuiltinStubContent, setBuiltinLocale } = require("../server/builtin-functions");

const constant = getBuiltinFunction("glua.event.events.progress_function_error");
const constantStub = makeBuiltinStubContent("glua.event.events.progress_function_error", constant);
assert(constantStub.includes("glua.event.events.progress_function_error = \"glua.event.events.progress_function_error\""));
assert(!constantStub.includes("function glua.event.events.progress_function_error()"));

const fn = getBuiltinFunction("print");
const functionStub = makeBuiltinStubContent("print", fn);
assert(functionStub.includes("function print()"));

setBuiltinLocale("zh-CN");
const eventFunction = getBuiltinFunction("glua.event.setProgress");
const eventStub = makeBuiltinStubContent("glua.event.setProgress", eventFunction);
assert(eventFunction.description.includes("预设事件的触发时机"));
assert(eventStub.includes("progress.function_error 在函数错误被 pcall/xpcall 捕获前触发"));
assert(eventStub.includes("此函数仅作为语言服务器跳转目标"));
setBuiltinLocale("en");

console.log("builtin stub tests passed");
