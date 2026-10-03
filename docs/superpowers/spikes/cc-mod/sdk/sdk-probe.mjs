// S10 probe: does a mod load in an Agent SDK run?
//
// Usage (from this directory, after `npm install`):
//   ANTHROPIC_BASE_URL=http://127.0.0.1:3500 ANTHROPIC_AUTH_TOKEN=magpie \
//     node sdk-probe.mjs /abs/path/to/docs/superpowers/spikes/cc-mod
//
// The recorder's log should then hold a session.start line whose
// isInteractive is false; its absence means the mod did not load.

import { query } from "@anthropic-ai/claude-agent-sdk";

const modPath = process.argv[2];
if (!modPath) {
  console.error("usage: node sdk-probe.mjs <abs path to cc-mod>");
  process.exit(1);
}

const q = query({
  prompt: "say hi",
  options: {
    model: "group/queqiao",
    plugins: [{ type: "local", path: modPath }],
    allowedTools: [],
  },
});

for await (const message of q) {
  if (message.type === "assistant" && message.message?.content) {
    for (const block of message.message.content) {
      if (block.type === "text") console.log(block.text);
    }
  }
}
