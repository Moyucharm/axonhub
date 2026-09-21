# TypeSafe System One API 参考

## 概述

AxonHub 提供 TypeSafe AI 的 Jev 原生 System One 协议。System One 是评估协议而不是聊天协议：请求提交共享 `state` 和带类型的 `questions`，Jev 返回结构化决策与概率。

## 端点

- `POST /v1/systemone`

调用 AxonHub 时使用与其他公开 API 相同的 Bearer API Key 认证。

## 渠道端点配置

在包含 Jev 模型的现有渠道中添加自定义端点：

| 字段 | 值 |
|---|---|
| API format | `typesafe/systemone` |
| Base URL | `https://api.typesafe.ai/v1` |
| Path | `/systemone`（可选；这是默认路径） |
| 渠道凭证 | TypeSafe API Key |

该端点必须显式启用。若渠道未配置 `typesafe/systemone`，System One 请求绝不会回退并发送到 chat、responses 或 messages 端点。

## 请求

```bash
curl http://localhost:8090/v1/systemone \
  -H "Authorization: Bearer $AXONHUB_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "jev-latest",
    "state": "Help! My payouts have been failing for 3 days.",
    "questions": {
      "is_urgent": {
        "type": "noul",
        "instructions": "Does this convey urgency?"
      }
    }
  }'
```

AxonHub 使用顶层 `model` 进行路由，并应用正常的模型映射。`state`、`questions` 和未知扩展字段保持原样转发。

## 响应

```json
{
  "model": "jev-1.13.0",
  "answers": {
    "is_urgent": {
      "type": "noul",
      "noul": 0.95
    }
  },
  "usage": {
    "input_tokens": 296,
    "output_tokens": 20
  }
}
```

提供商响应原样返回。AxonHub 同时把 TypeSafe usage 映射为内部输入、输出和总 token 计数，用于请求记录、限流统计和成本计算。

## 支持的 Jev 模型名

TypeSafe 当前文档列出 `jev-latest`、`jev-preview` 和 `jev-1.13.0` 等版本化 ID。请把需要使用的 ID 加入渠道支持模型列表和 AxonHub 模型关联。

## 限制

- TypeSafe System One 不支持流式响应。
- 本端点不会把 chat/messages 请求自动转换为 System One questions。
- AxonHub 的 `/v1/models` 仍返回统一模型目录；本功能不会代理 TypeSafe 提供商侧的模型列表端点。
