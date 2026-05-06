# Lingma Tap

Lingma Tap 是一个专为 Lingma API 设计的数据包可视化与抓取工具。它受 `cursor-tap` 启发，旨在帮助开发者直观地分析和调试 Lingma 的通信流量。

## 功能特性

- **流量抓取**：内置 MITM (Man-in-the-Middle) 代理，支持拦截和解析 HTTPS 流量。
- **自动解密**：集成 `QoderEncoding` 逻辑，自动解码 Lingma 特有的 Base64 混淆载荷（当 URL 中包含 `Encode=1` 时）。
- **实时监控**：基于 WebSocket 的实时数据流展示。
- **持久化存储**：使用 SQLite 存储抓取记录，支持历史回溯。
- **跨平台界面**：基于 Wails v2 + React + TypeScript 构建，提供原生应用体验。
- **现代 UI**：包含记录列表、详细面板、JSON 查看器等。

## 技术栈

- **后端**: Go, Wails v2, SQLite
- **前端**: React 19, TypeScript, Vite, Tailwind CSS
- **核心逻辑**: 自研 MITM 代理、Qoder 解码算法

## 快速开始

### 环境依赖

- [Go](https://golang.org/dl/) (1.25+)
- [Node.js](https://nodejs.org/) (20+)
- [Wails CLI](https://wails.io/docs/gettingstarted/installation) (`go install github.com/wailsapp/wails/v2/cmd/wails@latest`)

### 开发运行

1. 克隆仓库：
   ```bash
   git clone git@github.com:coolxll/lingma-tap.git
   cd lingma-tap
   ```

2. 启动开发模式：
   ```bash
   wails dev
   ```

### 编译打包

- **macOS**: `wails build` (生成 `.app` 文件)
- **Windows**: `wails build -platform windows/amd64` (生成 `.exe` 文件)

## GitHub Actions

项目配置了自动化的 CI/CD 流程：
- 每次推送代码会自动触发编译。
- 推送 `v*` 格式的标签（如 `v0.1.0`）会自动创建 GitHub Release，并附带 Windows (zip) 和 macOS (dmg) 的安装包。

## macOS 安装说明

由于本项目目前未加入 Apple Developer Program，因此导出的 DMG 在下载安装后可能会被 macOS 提示“无法打开”或“开发者身份不明”。

请按照以下步骤操作：
1. 将应用拖入 **Applications** 目录。
2. 在 **Applications** 中找到 **Lingma Tap**。
3. **右键点击**应用图标，选择 **打开 (Open)**。
4. 在弹出的对话框中再次点击 **打开 (Open)**。

或者，你可以在终端中运行以下命令来手动清除隔离标记：
```bash
xattr -cr /Applications/Lingma\ Tap.app
```

## 许可证

[MIT License](LICENSE) (如果适用)
