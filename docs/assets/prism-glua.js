(function (Prism) {
  "use strict";

  if (!Prism || !Prism.languages.lua) {
    return;
  }

  var glua = Prism.languages.extend("lua", {});
  glua.keyword = /\b(?:and|break|case|const|continue|default|do|else|elseif|end|false|for|function|goto|if|in|local|nil|not|or|repeat|return|switch|then|true|until|while)\b/;
  glua.builtin = /\b(?:assert|collectgarbage|coroutine|debug|dofile|error|getmetatable|glua|io|ipairs|load|loadfile|math|next|os|package|pairs|pcall|print|rawequal|rawget|rawlen|rawset|require|select|setmetatable|string|table|tonumber|tostring|type|utf8|xpcall)\b/;
  glua.constant = /\bprogress_(?:start|line|end|error|exit|function_(?:call|return|error|exit))\b/;
  glua["class-name"] = {
    pattern: /\bglua\.(?:codec|event|hash|json|path|regex|schema|toml|uuid|xml|yaml|zip)\b/,
    alias: "namespace",
  };

  Prism.languages.glua = glua;
})(typeof Prism === "undefined" ? undefined : Prism);
