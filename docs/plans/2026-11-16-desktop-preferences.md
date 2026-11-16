# Desktop Preferences Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 启动时直接隐藏到托盘，首次关闭时选择并记住关闭方式，偏好设置保存按钮统一靠右。

**Architecture:** 保留 `startMinimized` 存储字段兼容旧设置，使用 Wails `StartHidden` 控制启动窗口。新增关闭方式确认标记，由桌面控制器拦截首次关闭并通知 React 弹窗，选择成功保存后执行关闭或隐藏；取消与保存失败保持窗口。托盘失败或管理员重启显示窗口。

**Tech Stack:** Go、Wails v2、React、TypeScript、CSS。

---

### Task 1: Startup and close decisions

- Modify `desktop_system.go`, `main.go`, `app.go`, `internal/panel/model.go`.
- Replace taskbar minimization with hidden startup; wait for desktop initialization and restore the window if initialization/tray creation fails.
- Add `closeBehaviorConfirmed`, a pending close choice, cancellation, validation, and persistence before applying the selected behavior. Explicit exit always bypasses the prompt.
- Extend `tests/app/desktop_system_test.go` and `tests/panel/system_settings_test.go` to cover startup, first close, cancellation, unavailable tray, save failure, and persistence across reloads.
- Run `./tests/run.ps1 -Filter 'TestDesktop|TestSystemPreference|TestSystemSettings'`.

### Task 2: Preference UI and first-close dialog

- Modify `frontend/src/App.tsx`, `frontend/src/api.ts`, `frontend/src/styles.css`; add `frontend/src/ClosePrompt.tsx`.
- Rename the option to 静默启动 with tray-specific explanatory text; show 首次关闭时询问 before a choice is confirmed.
- Provide explicit 退出程序 and 最小化到托盘 buttons, cancellation, loading/error states, keyboard focus containment, and a disabled tray option if unavailable.
- Use one right-aligned action row for system and network saves; appearance saves immediately and storage has no save action.
- Run `npm.cmd run build` and visually inspect both preference tabs and the close dialog in the browser preview.

### Task 3: Documentation and final verification

- Update `docs/system-settings.md` with startup and first-close behavior.
- Run `./tests/run.ps1 -Vet`, inspect the diff, and compile the Windows desktop application.
- Preserve pre-existing service changes and avoid modifying real autorun registry entries or stopping user instances during verification.
