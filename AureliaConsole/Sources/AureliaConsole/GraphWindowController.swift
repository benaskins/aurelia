import AppKit
import SwiftUI

@MainActor
final class GraphWindowController {
    private var window: NSPanel?
    private let graphStore: GraphStore
    private var closeObserver: Any?

    init(graphStore: GraphStore) {
        self.graphStore = graphStore
    }

    func showWindow() {
        if let window, window.isVisible {
            NSApp.setActivationPolicy(.regular)
            NSApp.activate()
            window.makeKeyAndOrderFront(nil)
            return
        }

        let panel = NSPanel(
            contentRect: NSRect(x: 0, y: 0, width: 900, height: 550),
            styleMask: [.titled, .closable, .resizable, .utilityWindow],
            backing: .buffered,
            defer: false
        )
        panel.title = "Aurelia — Dependency Graph"
        panel.titlebarAppearsTransparent = true
        panel.backgroundColor = NSColor(LaminaTheme.bg)
        panel.isMovableByWindowBackground = true
        panel.minSize = NSSize(width: 500, height: 300)
        panel.isReleasedWhenClosed = false
        panel.isFloatingPanel = false

        let hostingView = NSHostingView(rootView: DependencyGraphView(graphStore: graphStore))
        panel.contentView = hostingView
        panel.center()

        self.window = panel

        // Become a regular app so macOS lets us foreground the window
        NSApp.setActivationPolicy(.regular)
        NSApp.activate()
        panel.makeKeyAndOrderFront(nil)

        // Revert to accessory (hide dock icon) when window closes
        closeObserver = NotificationCenter.default.addObserver(
            forName: NSWindow.willCloseNotification,
            object: panel,
            queue: .main
        ) { [weak self] _ in
            Task { @MainActor in
                self?.windowDidClose()
            }
        }

        graphStore.startPolling()
    }

    private func windowDidClose() {
        if let closeObserver {
            NotificationCenter.default.removeObserver(closeObserver)
            self.closeObserver = nil
        }
        NSApp.setActivationPolicy(.accessory)
    }

    func closeWindow() {
        window?.close()
    }
}
