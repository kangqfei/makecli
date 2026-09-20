# Context SDK 窗口接入

设备 daemon 按同一执行身份读取 Fill 补充材料，再经 Read 获取当前 Turn 和输入原文，分页保持完整文字及输入顺序。缺少输入、跨 Namespace 或分页异常时停止，不以历史替代当前问题。原生 CLI 尚未开放受控模型循环，因此不宣称支持平台 Compact 或两个检索工具；该边界与 ContextSDK 接入文档一致。

验证覆盖 daemon 生命周期、完整当前输入、执行身份头和文本工具历史；发布不修改公共 Make 资源命令。
