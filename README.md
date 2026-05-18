# AIYolo

AIYolo 是一个“云原生 Agentic IDE + 混合计算架构”工作空间。目标是让用户在浏览器中完成 AI 开发、实验、数据分析和代码协作，同时把云端隔离算力、本地资源、安全授权、Agent 自动化和实时可观测性组织成一个可实现、可扩展的系统。

## License

本项目采用 GNU Affero General Public License v3.0 许可协议发布，详见 LICENSE。

## Console Chat

控制台左侧导航提供独立的 Chat 入口，会在新标签页打开 `/console/chat`。当前实现只保留控制台原生 chat 工作区，也不再包含 monorepo 内的前端前置子项目。

- 入口路径是 `/console/chat`。
- 使用控制台内置的服务端渲染 chat 页面和流式接口。
- 复用现有 provider、模型路由、代理、usage 和 audit 链路。
- 本地会话历史保存在浏览器里，支持重命名、删除和多标签页同步。
- 支持附件上传；多模态请求会按当前路由协议自动组装 content parts。
- 当前保留的对话接口是 `POST /console/chat` 和 `POST /console/chat/stream`。
- 附件上传接口是 `POST /console/chat/attachments`，对象存储走独立的 `chat.attachments.*` 配置。

Chat 页面现在已经切成独立的 Gemini 风格工作区，并限制到当前允许的模型集合。下一步会继续补会话搜索、附件清理和更细的多模态能力边界。
