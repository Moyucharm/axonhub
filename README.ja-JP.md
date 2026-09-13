<div align="center">

# AxonHub - オールインワンAI開発プラットフォーム
### あらゆるSDKを使用。あらゆるモデルにアクセス。コード変更ゼロ。

<a href="https://trendshift.io/repositories/16225" target="_blank"><img src="https://trendshift.io/api/badge/repositories/16225" alt="looplj%2Faxonhub | Trendshift" style="width: 250px; height: 55px;" width="250" height="55"/></a>

</div>

<div align="center">

[![Test Status](https://github.com/looplj/axonhub/actions/workflows/test.yml/badge.svg)](https://github.com/looplj/axonhub/actions/workflows/test.yml)
[![Lint Status](https://github.com/looplj/axonhub/actions/workflows/lint.yml/badge.svg)](https://github.com/looplj/axonhub/actions/workflows/lint.yml)
[![Go Version](https://img.shields.io/github/go-mod/go-version/looplj/axonhub?logo=go&logoColor=white)](https://golang.org/)
[![Docker Ready](https://img.shields.io/badge/docker-ready-2496ED?logo=docker&logoColor=white)](https://docker.com)

[English](README.en-US.md) | [中文](README.md) | [日本語](README.ja-JP.md)

</div>

---

## 📖 プロジェクト紹介

### オールインワンAI開発プラットフォーム

**AxonHubは、コードを一行も変更することなくモデルプロバイダーを切り替えられるAIゲートウェイです。**

OpenAI SDK、Anthropic SDK、またはその他のAI SDKを使用している場合でも、AxonHubはリクエストを透過的に変換し、サポートされているあらゆるモデルプロバイダーで動作させます。リファクタリングもSDKの入れ替えも不要 - 設定を変更するだけで完了です。

**解決する課題：**
- 🔒 **ベンダーロックイン** - GPT-4からClaudeやGeminiへ瞬時に切り替え
- 🔧 **統合の複雑さ** - 10以上のプロバイダーに対して単一のAPIフォーマット
- 📊 **オブザーバビリティの不足** - すぐに使えるリクエストトレーシング
- 💸 **コスト管理** - リアルタイムの使用量追跡と予算管理

<div align="center">
  <img src="docs/axonhub-architecture-light.svg" alt="AxonHub Architecture" width="700"/>
</div>

### コア機能

| 機能 | 提供する価値 |
|---------|-------------|
| 🔄 [**あらゆるSDK → あらゆるモデル**](docs/en/api-reference/openai-api.md) | OpenAI SDKでClaudeを呼び出したり、Anthropic SDKでGPTを呼び出したり。コード変更不要。 |
| 🔍 [**完全なリクエストトレーシング**](docs/en/guides/tracing.md) | スレッド対応のオブザーバビリティで完全なリクエストタイムラインを提供。デバッグを高速化。 |
| 🔐 [**エンタープライズRBAC**](docs/en/guides/permissions.md) | きめ細かなアクセス制御、使用量クォータ、データ分離。 |
| ⚡ [**スマートロードバランシング**](docs/en/guides/load-balance.md) | 100ms未満の自動フェイルオーバー。常に最も正常なチャネルにルーティング。 |
| 💰 [**リアルタイムコスト追跡**](docs/en/guides/cost-tracking.md) | リクエストごとのコスト内訳。入力、出力、キャッシュトークン - すべて追跡。 |

---

## 📚 ドキュメント

詳細な技術ドキュメント、APIリファレンス、アーキテクチャ設計などについては、以下をご覧ください
- [![DeepWiki](https://img.shields.io/badge/DeepWiki-looplj%2Faxonhub-blue.svg?logo=data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAACwAAAAyCAYAAAAnWDnqAAAAAXNSR0IArs4c6QAAA05JREFUaEPtmUtyEzEQhtWTQyQLHNak2AB7ZnyXZMEjXMGeK/AIi+QuHrMnbChYY7MIh8g01fJoopFb0uhhEqqcbWTp06/uv1saEDv4O3n3dV60RfP947Mm9/SQc0ICFQgzfc4CYZoTPAswgSJCCUJUnAAoRHOAUOcATwbmVLWdGoH//PB8mnKqScAhsD0kYP3j/Yt5LPQe2KvcXmGvRHcDnpxfL2zOYJ1mFwrryWTz0advv1Ut4CJgf5uhDuDj5eUcAUoahrdY/56ebRWeraTjMt/00Sh3UDtjgHtQNHwcRGOC98BJEAEymycmYcWwOprTgcB6VZ5JK5TAJ+fXGLBm3FDAmn6oPPjR4rKCAoJCal2eAiQp2x0vxTPB3ALO2CRkwmDy5WohzBDwSEFKRwPbknEggCPB/imwrycgxX2NzoMCHhPkDwqYMr9tRcP5qNrMZHkVnOjRMWwLCcr8ohBVb1OMjxLwGCvjTikrsBOiA6fNyCrm8V1rP93iVPpwaE+gO0SsWmPiXB+jikdf6SizrT5qKasx5j8ABbHpFTx+vFXp9EnYQmLx02h1QTTrl6eDqxLnGjporxl3NL3agEvXdT0WmEost648sQOYAeJS9Q7bfUVoMGnjo4AZdUMQku50McDcMWcBPvr0SzbTAFDfvJqwLzgxwATnCgnp4wDl6Aa+Ax283gghmj+vj7feE2KBBRMW3FzOpLOADl0Isb5587h/U4gGvkt5v60Z1VLG8BhYjbzRwyQZemwAd6cCR5/XFWLYZRIMpX39AR0tjaGGiGzLVyhse5C9RKC6ai42ppWPKiBagOvaYk8lO7DajerabOZP46Lby5wKjw1HCRx7p9sVMOWGzb/vA1hwiWc6jm3MvQDTogQkiqIhJV0nBQBTU+3okKCFDy9WwferkHjtxib7t3xIUQtHxnIwtx4mpg26/HfwVNVDb4oI9RHmx5WGelRVlrtiw43zboCLaxv46AZeB3IlTkwouebTr1y2NjSpHz68WNFjHvupy3q8TFn3Hos2IAk4Ju5dCo8B3wP7VPr/FGaKiG+T+v+TQqIrOqMTL1VdWV1DdmcbO8KXBz6esmYWYKPwDL5b5FA1a0hwapHiom0r/cKaoqr+27/XcrS5UwSMbQAAAABJRU5ErkJggg==)](https://deepwiki.com/looplj/axonhub)
- [![zread](https://img.shields.io/badge/Ask_Zread-_.svg?style=flat&color=00b0aa&labelColor=000000&logo=data%3Aimage%2Fsvg%2Bxml%3Bbase64%2CPHN2ZyB3aWR0aD0iMTYiIGhlaWdodD0iMTYiIHZpZXdCb3g9IjAgMCAxNiAxNiIgZmlsbD0ibm9uZSIgeG1sbnM9Imh0dHA6Ly93d3cudzMub3JnLzIwMDAvc3ZnIj4KPHBhdGggZD0iTTQuOTYxNTYgMS42MDAxSDIuMjQxNTZDMS44ODgxIDEuNjAwMSAxLjYwMTU2IDEuODg2NjQgMS42MDE1NiAyLjI0MDFWNC45NjAxQzEuNjAxNTYgNS4zMTM1NiAxLjg4ODEgNS42MDAxIDIuMjQxNTYgNS42MDAxSDQuOTYxNTZDNS4zMTUwMiA1LjYwMDEgNS42MDE1NiA1LjMxMzU2IDUuNjAxNTYgNC45NjAxVjIuMjQwMUM1LjYwMTU2IDEuODg2NjQgNS4zMTUwMiAxLjYwMDEgNC45NjE1NiAxLjYwMDFaIiBmaWxsPSIjZmZmIi8%2BCjxwYXRoIGQ9Ik00Ljk2MTU2IDEwLjM5OTlIMi4yNDE1NkMxLjg4ODEgMTAuMzk5OSAxLjYwMTU2IDEwLjY4NjQgMS42MDE1NiAxMS4wMzk5VjEzLjc1OTlDMS42MDE1NiAxNC4xMTM0IDEuODg4MSAxNC4zOTk5IDIuMjQxNTYgMTQuMzk5OUg0Ljk2MTU2QzUuMzE1MDIgMTQuMzk5OSA1LjYwMTU2IDE0LjExMzQgNS42MDE1NiAxMy43NTk5VjExLjAzOTlDNS42MDE1NiAxMC42ODY0IDUuMzE1MDIgMTAuMzk5OSA0Ljk2MTU2IDEwLjM5OTlaIiBmaWxsPSIjZmZmIi8%2BCjxwYXRoIGQ9Ik0xMy43NTg0IDEuNjAwMUgxMS4wMzg0QzEwLjY4NSAxLjYwMDEgMTAuMzk4NCAxLjg4NjY0IDEwLjM5ODQgMi4yNDAxVjQuOTYwMUMxMC4zOTg0IDUuMzEzNTYgMTAuNjg1IDUuNjAwMSAxMS4wMzg0IDUuNjAwMUgxMy43NTg0QzE0LjExMTkgNS42MDAxIDE0LjM5ODQgNS4zMTM1NiAxNC4zOTg0IDQuOTYwMVYyLjI0MDFDMTQuMzk4NCAxLjg4NjY0IDE0LjExMTkgMS42MDAxIDEzLjc1ODQgMS42MDAxWiIgZmlsbD0iI2ZmZiIvPgo8cGF0aCBkPSJNNCAxMkwxMiA0TDQgMTJaIiBmaWxsPSIjZmZmIi8%2BCjxwYXRoIGQ9Ik00IDEyTDEyIDQiIHN0cm9rZT0iI2ZmZiIgc3Ryb2tlLXdpZHRoPSIxLjUiIHN0cm9rZS1saW5lY2FwPSJyb3VuZCIvPgo8L3N2Zz4K&logoColor=ffffff)](https://zread.ai/looplj/axonhub)

---

## 🎯 デモ

[デモインスタンス](https://axonhub.onrender.com)でAxonHubをお試しください！

**注意**：デモインスタンスでは現在、ZhipuとOpenRouterの無料モデルが設定されています。

### デモアカウント

- **メールアドレス**: demo@example.com
- **パスワード**: 12345678

---

## ⭐ 機能

### 📸 スクリーンショット

AxonHubの動作画面をご覧ください：

<table>
  <tr>
    <td align="center">
      <a href="docs/screenshots/axonhub-dashboard.png">
        <img src="docs/screenshots/axonhub-dashboard.png" alt="System Dashboard" width="250"/>
      </a>
      <br/>
      システムダッシュボード
    </td>
    <td align="center">
      <a href="docs/screenshots/axonhub-channels.png">
        <img src="docs/screenshots/axonhub-channels.png" alt="Channel Management" width="250"/>
      </a>
      <br/>
      チャネル管理
    </td>
    <td align="center">
      <a href="docs/screenshots/axonhub-model-price.png">
        <img src="docs/screenshots/axonhub-model-price.png" alt="Model Price" width="250"/>
      </a>
      <br/>
      モデル料金
    </td>
  </tr>
  <tr>
  <td align="center">
      <a href="docs/screenshots/axonhub-models.png">
        <img src="docs/screenshots/axonhub-models.png" alt="Models" width="250"/>
      </a>
      <br/>
      モデル
    </td>
    <td align="center">
      <a href="docs/screenshots/axonhub-trace.png">
        <img src="docs/screenshots/axonhub-trace.png" alt="Trace Viewer" width="250"/>
      </a>
      <br/>
      トレースビューア
    </td>
    <td align="center">
      <a href="docs/screenshots/axonhub-requests.png">
        <img src="docs/screenshots/axonhub-requests.png" alt="Request Monitoring" width="250"/>
      </a>
      <br/>
      リクエストモニタリング
    </td>
  </tr>
</table>

---

### 🚀 APIタイプ

| APIタイプ             | ステータス     | 説明                    | ドキュメント                                     |
| -------------------- | ---------- | ------------------------------ | -------------------------------------------- |
| **テキスト生成**  | ✅ 完了    | 会話インターフェース       | [OpenAI API](docs/en/api-reference/openai-api.md), [Anthropic API](docs/en/api-reference/anthropic-api.md), [Gemini API](docs/en/api-reference/gemini-api.md) |
| **画像生成** | ✅ 完了 | 画像生成               | [Image Generation](docs/en/api-reference/image-generation.md) |
| **リランク**           | ✅ 完了    | 結果のランキング                | [Rerank API](docs/en/api-reference/rerank-api.md) |
| **エンベディング**        | ✅ 完了    | ベクトルエンベディング生成    | [Embedding API](docs/en/api-reference/embedding-api.md) |
| **リアルタイム**         | 📝 予定    | リアルタイム会話機能 | -                                            |

---

### 🤖 対応プロバイダー

| プロバイダー               | ステータス     | 対応モデル             | 互換API |
| ---------------------- | ---------- | ---------------------------- | --------------- |
| **OpenAI**             | ✅ 完了    | GPT-4, GPT-4o, GPT-5など   | OpenAI, Anthropic, Gemini, Embedding, Image Generation |
| **Anthropic**          | ✅ 完了    | Claude 3.5, Claude 3.0など | OpenAI, Anthropic, Gemini |
| **Zhipu AI**           | ✅ 完了    | GLM-4.5, GLM-4.5-airなど   | OpenAI, Anthropic, Gemini |
| **Moonshot AI (Kimi)** | ✅ 完了    | kimi-k2など                | OpenAI, Anthropic, Gemini |
| **DeepSeek**           | ✅ 完了    | DeepSeek-V3.1など          | OpenAI, Anthropic, Gemini |
| **ByteDance Doubao**   | ✅ 完了    | doubao-1.6など             | OpenAI, Anthropic, Gemini, Image Generation |
| **Gemini**             | ✅ 完了    | Gemini 2.5など             | OpenAI, Anthropic, Gemini, Image Generation |
| **Fireworks**          | ✅ 完了    | MiniMax-M2.5, GLM-5, Kimi K2.5など | OpenAI |
| **Jina AI**            | ✅ 完了    | Embeddings, Rerankerなど   | Jina Embedding, Jina Rerank |
| **OpenRouter**         | ✅ 完了    | 各種モデル               | OpenAI, Anthropic, Gemini, Image Generation |
| **ZAI**                | ✅ 完了    | -                            | Image Generation |
| **AWS Bedrock**        | 🔄 テスト中 | Claude on AWS                | OpenAI, Anthropic, Gemini |
| **Google Cloud**       | 🔄 テスト中 | Claude on GCP                | OpenAI, Anthropic, Gemini |
| **NanoGPT**            | ✅ 完了    | 各種モデル、画像生成    | OpenAI, Anthropic, Gemini, Image Generation |

---

## 🚀 デプロイガイド

AxonHub をデプロイするには、Agent に [`deploy-axonhub`](https://github.com/looplj/axonhub-skills/blob/main/deploy-axonhub/SKILL.md) を読み、その手順に従うよう依頼してください。

---

## ⚡ クイックスタート

デプロイ完了後：

1. **システムを初期化する**
   - Agent から返された AxonHub の URL を開きます。
   - 初期化ウィザードに従って管理者アカウントを作成します。
2. **チャネルを追加する**
   - **Channels（チャネル）** で AI プロバイダーと API キーを追加します。
   - 対応モデルを設定し、接続をテストしてチャネルを有効にします。
3. **API キーを作成する**
   - **API Keys** でクライアント用の AxonHub API キーを作成します。
4. **最初のリクエストを送信する**

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://localhost:8090/v1",
    api_key="your-axonhub-api-key"
)

response = client.chat.completions.create(
    model="gpt-4o",
    messages=[{"role": "user", "content": "Hello, AxonHub!"}]
)

print(response.choices[0].message.content)
```

設定の詳細とその他の API 例については、[ドキュメント一覧](docs/en/index.md)を参照してください。

---

## 🛠️ 開発ガイド

詳細な開発手順、アーキテクチャ設計、コントリビューションガイドラインについては、[docs/en/development/development.md](docs/en/development/development.md)を参照してください。

---

## 👥 チーム

<table>
  <tr>
    <td align="center">
      <a href="https://github.com/looplj">
        <img src="https://github.com/looplj.png?size=100" width="100" alt="looplj"/><br/>
        <sub><b>looplj</b></sub>
      </a>
    </td>
    <td align="center">
      <a href="https://github.com/llc1123">
        <img src="https://github.com/llc1123.png?size=100" width="100" alt="llc1123"/><br/>
        <sub><b>llc1123</b></sub>
      </a>
    </td>
  </tr>
</table>

---

## 🤝 謝辞

- 🙏 [musistudio/llms](https://github.com/musistudio/llms) - LLM変換フレームワーク、インスピレーションの源
- 🎨 [satnaing/shadcn-admin](https://github.com/satnaing/shadcn-admin) - 管理画面テンプレート
- 🔧 [99designs/gqlgen](https://github.com/99designs/gqlgen) - GraphQLコード生成
- 🌐 [gin-gonic/gin](https://github.com/gin-gonic/gin) - HTTPフレームワーク
- 🗄️ [ent/ent](https://github.com/ent/ent) - ORMフレームワーク
- 🔧 [air-verse/air](https://github.com/air-verse/air) - Goサービスの自動リロード
- ☁️ [Render](https://render.com) - デモをホスティングする無料クラウドデプロイプラットフォーム
- 🗃️ [TiDB Cloud](https://www.pingcap.com/tidb-cloud/) - デモデプロイ用のサーバーレスデータベースプラットフォーム

---

## 📄 ライセンス

このプロジェクトは複数のライセンス（Apache-2.0およびLGPL-3.0）の下でライセンスされています。詳細なライセンスの概要と条項については、[LICENSE](LICENSE)ファイルを参照してください。

---

<div align="center">

**AxonHub** - オールインワンAI開発プラットフォーム、AI開発をよりシンプルに

[🏠 ホームページ](https://github.com/looplj/axonhub) • [📚 ドキュメント](https://deepwiki.com/looplj/axonhub) • [🐛 問題報告](https://github.com/looplj/axonhub/issues)

AxonHubチームが ❤️ を込めて開発

</div>
