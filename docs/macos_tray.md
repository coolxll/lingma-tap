# macOS 托盘实现记录

## 当前约定

- Bundle ID 固定为 `com.coolxll.lingma-tap`。
- `build/darwin/Info.plist` 和 `build/darwin/Info.dev.plist` 都是项目文件，不能依赖 Wails 自动生成的默认 plist。
- 两份 plist 均不设置 `LSUIElement`。应用启动时由 Cocoa 代码先切换到 `NSApplicationActivationPolicyAccessory` 创建 `NSStatusItem`；主窗口成为 key window 后再切回 `NSApplicationActivationPolicyRegular`。
- CI 使用与 `go.mod` 一致的 Wails CLI `v2.12.0`，避免 clean checkout 生成不同的 Bundle ID 或应用模式。

## 图标与状态项

托盘图标来自 `assets/tray_icon.png`，作为 template image 设置到 `NSStatusItem.button`。状态项使用固定 `24pt` 宽度、`18pt` 图标、`NSImageOnly` 和按比例缩放，避免空标题配合 `NSVariableStatusItemLength` 时出现零宽状态项。

状态项只有在 `NSStatusItem` 和 button 都创建成功后才标记为已创建；失败时保留后续回调重试的机会。

## 诊断日志

使用统一日志查看托盘专用记录：

```bash
log show --style compact --last 10m \
  --predicate 'eventMessage CONTAINS "LingmaTap-Tray"'
```

正常启动应看到 `image=1`、`visible=1`、`frame=24.0x22.0`、`screen=1` 和 `imageSize=18.0x18.0`。如果 `screen=0` 或 frame 为零，说明状态项尚未挂到菜单栏；如果这些值正常但用户仍看不到图标，应检查 macOS 菜单栏溢出或第三方菜单栏管理器。

## 构建验证

```bash
wails build -platform darwin/arm64 -clean
plutil -p build/bin/lingma-tap.app/Contents/Info.plist
go test ./...
```

构建产物应报告 `CFBundleIdentifier = com.coolxll.lingma-tap`，且 plist 中不应出现 `LSUIElement`。
