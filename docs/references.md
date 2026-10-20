# Source references and licensing

Chaoxing's original project code is licensed under Apache-2.0. The following MIT-licensed projects were consulted as protocol references:

- [DanMo661/Xuexitong-mcp](https://github.com/DanMo661/Xuexitong-mcp): upstream interface and MCP usage reference.
- [menghuanshiguang/chaoxing-cli](https://github.com/menghuanshiguang/chaoxing-cli): upstream login and CLI protocol reference.

Their source code was not copied into this project. These links acknowledge protocol research, not a bundled dependency or a claim that their MIT code was relicensed. Go dependencies retain their own licenses. Any future copied or vendored material must retain the applicable license and attribution.

Private deployment patterns are not public source dependencies. Public documentation contains only synthetic examples and project contracts, not private repository code, operational records or infrastructure details.

CLI design also draws on [Lark CLI](https://github.com/larksuite/cli) (MIT), [Command Line Interface Guidelines](https://clig.dev/), [OpenAI tool metadata guidance](https://developers.openai.com/plugins/guides/optimize-metadata), and the [MCP specification](https://modelcontextprotocol.io/). These informed the original operation registry and adapter boundaries; no reference framework is vendored or required at runtime.
