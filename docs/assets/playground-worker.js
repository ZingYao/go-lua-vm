(function () {
  "use strict";

  var queuedMessages = [];
  var runtimeReady = false;

  self.onmessage = function (event) {
    if (!runtimeReady || typeof self.gluaPlaygroundDispatch !== "function") {
      queuedMessages.push(event.data);
      return;
    }
    self.gluaPlaygroundDispatch(event.data);
  };

  function dispatchQueuedMessages() {
    runtimeReady = true;
    while (queuedMessages.length > 0) {
      self.gluaPlaygroundDispatch(queuedMessages.shift());
    }
  }

  function instantiateWasm(go) {
    return fetch("glua.wasm").then(function (response) {
      if (!response.ok) {
        throw new Error("加载 glua.wasm 失败：HTTP " + response.status);
      }
      if (WebAssembly.instantiateStreaming) {
        return WebAssembly.instantiateStreaming(response.clone(), go.importObject).catch(function () {
          return response.arrayBuffer().then(function (bytes) {
            return WebAssembly.instantiate(bytes, go.importObject);
          });
        });
      }
      return response.arrayBuffer().then(function (bytes) {
        return WebAssembly.instantiate(bytes, go.importObject);
      });
    });
  }

  importScripts("wasm_exec.js");
  var go = new Go();
  go.argv = ["glua-wasm"];
  instantiateWasm(go)
    .then(function (result) {
      var runPromise = go.run(result.instance);
      dispatchQueuedMessages();
      return runPromise;
    })
    .catch(function (error) {
      self.postMessage({
        type: "bootstrapError",
        stream: "stderr",
        text: String(error && error.message ? error.message : error) + "\n",
      });
    });
})();
