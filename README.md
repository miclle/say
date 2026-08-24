# say

`say` 是一个用 Go 编写的 macOS 文档与网页朗读终端程序。它读取本地 UTF-8 文本文档，或下载 HTTP(S) 网页并用 [readability.go](https://github.com/miclle/readability.go) 提取正文，再按自然大段落和 TTS 单次调用上限组织内容。第一段音频就绪后立即显示并播放，后续段落按顺序在后台合成。播放过程中可以暂停，也可以按时间前后跳转。交互式启动时可以在 TUI 中选择 macOS 系统 TTS 或实验性的 Microsoft Edge TTS。

```text
say  lesson.txt
TTS  macOS say (system voice) · 3 speech units

… preparing audio · 0/3 ready
… ready to play · 1/3 prepared
Space Play/Pause · ← Back 5s · → Forward 5s

[1/3] ✓ 第一句已经播放完成。
[2/3] ▶ The system voice is reading this chapter.
[3/3] · 下一章节等待播放。
```

## 功能

- 打开任意本地 UTF-8 文本文档；`.txt` 按原文朗读，`.md` 和 `.markdown` 会先转换为适合朗读的语义文本。
- 直接打开 `http://` 或 `https://` 网页；自动移除导航、脚本等非正文内容，使用提取出的文章标题和自然段进入播放链路。
- stdout 连接终端时，用英文单行动态加载状态展示文件读取、文档解析、网页读取和正文提取过程；输出重定向时不增加 spinner 或 ANSI 控制字符。
- Markdown 会移除 front matter、标题与强调标记、列表符号、链接地址、任务框和 HTML 标签；表格按行组织，结构化代码块会跳过，`text`/`plaintext` 代码块保留正文。
- 交互式终端中如果没有传 `--provider`，会先显示 TTS provider 选择器；按上下方向键切换，按 Enter 确认。
- 选择 `system` 时调用 macOS `/usr/bin/say`，使用“系统设置”中选择的声音和语速；每个自然段或限长片段会独立合成为临时 AIFF 音频。
- 选择 `edge` 时使用实验性的 Microsoft Edge Read Aloud 在线服务并合成 MP3 音频；默认声音为 `zh-CN-XiaoxiaoNeural`。
- 优先按空行识别自然大段落，同一段落中的多个句子会连续播放，单换行会规范为空格。
- 只有自然段超过单次调用上限时，才按完整句子贪心组合为尽可能大的片段；超长单句再从逗号、分号、冒号、空格等位置切分。
- 默认保证每次 TTS 调用不超过 500 个 Unicode 字符，可按服务限制调整。
- 第一个音频片段就绪后由 AVFoundation 立即播放；后续片段按文档顺序后台合成，无需等待整篇文档。
- 如果顺序播放追上尚未合成的内容，终端会显示 buffering，目标片段就绪后保持原来的播放或暂停状态。
- 交互式 TUI 会维护一份连续且不重复的章节列表：已完成显示 `✓`，当前播放显示 `▶`，暂停显示 `⏸`，等待播放显示 `·`。活动章节的正文会使用终端反显呈现类似选中的效果，暂停时保留定位，播放完成或跳转后随活动状态更新。章节编号按总章节数补齐位数（例如 `[01/62]`），长内容折行后会对齐到播放图标后的正文首字。方向键跨段跳转时，活动状态会在列表中上下移动，中间章节不会从列表中消失。
- 交互式终端中按空格播放或暂停，按左方向键回退 5 秒，按右方向键快进 5 秒；跳转可以跨越自然段边界。
- 按 `Ctrl-C` 可中断当前播放。

## 使用

要求 Go 1.26 或更高版本、macOS，以及启用 CGO 的 Go 工具链。构建时需要系统 Clang；播放使用 AVFoundation，不需要安装第三方播放器。System provider 会调用 macOS 自带的 `/usr/bin/say`；Edge provider 需要访问 Microsoft 在线服务。

在项目目录中直接运行：

```bash
go run ./cmd/say -- ./notes.md
```

播放网页文章：

```bash
go run ./cmd/say -- https://example.com/article
```

也可以安装到 `GOBIN`：

```bash
go install ./cmd/say
say ./notes.md
```

安装后的命令名与 macOS 自带的 `/usr/bin/say` 相同，但本项目始终通过绝对路径调用系统程序，不会递归调用自身。

当 stdin 和 stdout 都连接到终端、且没有显式传入 `--provider` 时，会显示单行选择器：

```text
TTS provider  › macOS system TTS  (↑/↓ choose · Enter confirm)
```

按 `↑`/`↓` 选择 provider，按 Enter 确认，按 `Ctrl-C` 取消。`--provider system` 或 `--provider edge` 会跳过选择器；输入或输出被重定向时不会显示选择器，并继续默认使用 `system`，以保持脚本行为稳定。

### 参数

```text
Usage: say [flags] <document-or-url>

  -max-chars int
        maximum Unicode characters per TTS call (default 500)
  -no-color
        disable ANSI terminal colors
  -provider string
        TTS provider: system or edge (interactive: choose; non-interactive: system)
  -rate int
        system speech rate in words per minute (default: system rate)
  -speed float
        Edge TTS speed multiplier, from 0.5 to 2.0 (default 1)
  -voice string
        provider voice name (default: provider voice)
```

按特定 TTS 长度限制播放：

```bash
go run ./cmd/say --max-chars 300 ./notes.md
```

指定系统声音和每分钟单词数：

```bash
go run ./cmd/say --voice Tingting --rate 210 ./notes.md
```

可用声音可以通过系统命令查看：

```bash
/usr/bin/say -v '?'
```

使用 Microsoft Edge TTS 的默认中文声音：

```bash
go run ./cmd/say --provider edge ./notes.md
```

指定 Edge voice short name 和相对语速：

```bash
go run ./cmd/say --provider edge --voice en-US-AriaNeural --speed 1.25 ./notes.md
```

`--rate` 仅适用于 `system`，`--speed` 仅适用于 `edge`；显式传入不兼容的参数会直接报错。Edge provider 使用 Microsoft Edge Read Aloud 的在线接口，而不是需要 Azure key/region 的正式 Azure Speech API。该接口属于实验性能力，可能随上游服务变化而失效；提取后的文档或网页正文片段会发送给 Microsoft，请勿用于不应离开本机的敏感内容。System provider 不会把提取后的正文发送给第三方 TTS 服务。

### 播放快捷键

在 stdin 和 stdout 都连接到交互式终端时：

| 按键 | 操作 |
| --- | --- |
| 空格 | 播放 / 暂停 |
| `←` | 回退 5 秒 |
| `→` | 快进 5 秒 |
| `Ctrl-C` | 中断并退出 |

左右键作用于整篇文档的音频时间轴：到达段落边界时会切换音频，TUI 中的活动图标在连续章节列表内上下移动，不会追加重复章节、跳转状态行或方向键触发的 buffering 行；在暂停状态下跳转不会自动恢复播放。章节较多时，TUI 会显示包含当前章节的连续窗口。输出被重定向，或 stdin 不是终端时，程序会自动播放，但不会启用这些快捷键，并继续输出普通的顺序文本记录。

## 文档边界

本地输入必须是普通 UTF-8 文件且转换后仍有可朗读文字。Markdown 的标题、正文、列表、引用、表格、链接标签、图片 alt 文本和行内代码会保留语义内容；front matter、链接地址、裸 URL、HTML 标签以及 Go、Shell、JSON、Mermaid 等结构化代码块不会发送给 TTS。

远程输入仅支持绝对 `http://` 和 `https://` URL。程序会在本机下载网页 HTML，跟随标准 HTTP 重定向，按响应头或 HTML 元数据声明的字符集转换为 UTF-8，并使用 readability.go 提取标题和正文；请求超时为 15 秒，解压后的响应正文上限为 10 MiB，只接受 `text/html`、`application/xhtml+xml` 或未声明 `Content-Type` 的响应。HTTP 错误、不支持的内容类型、超限响应和无法提取正文的页面会在 TTS 初始化前返回错误。PDF 和 Word 解析尚未包含在当前版本中。

可定位音频播放当前只实现了 macOS。其他操作系统可以编译项目，但运行时会返回明确的不支持提示。临时 AIFF 或 MP3 音频存放在操作系统临时目录中，无论播放成功、失败还是被取消都会清理。每次 Edge TTS 网络调用最多等待 45 秒，并会随播放取消而终止。

## 开发验证

```bash
go test -race ./...
go vet ./...
go build ./...
```
