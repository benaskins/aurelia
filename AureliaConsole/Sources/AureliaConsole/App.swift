import SwiftUI
import AppKit

@main
struct AureliaConsoleApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) var appDelegate

    var body: some Scene {
        // Invisible settings window — the real UI is the NSStatusItem popover
        Settings {
            EmptyView()
        }
    }
}

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    private var statusItem: NSStatusItem!
    private var popover: NSPopover!
    private let store = ServiceStore()
    private var observeTask: Task<Void, Never>?
    private var lastStatus: ServiceStore.AggregateStatus?

    func applicationDidFinishLaunching(_ notification: Notification) {
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
        if let button = statusItem.button {
            button.image = MenuBarIcon.disconnected()
            button.action = #selector(togglePopover)
            button.target = self
        }

        let hostingView = NSHostingView(rootView: PopoverContentView(store: store))
        hostingView.frame = NSRect(x: 0, y: 0, width: 400, height: 460)

        popover = NSPopover()
        popover.contentSize = NSSize(width: 400, height: 460)
        popover.behavior = .transient
        popover.contentViewController = NSViewController()
        popover.contentViewController?.view = hostingView

        store.startPolling()
        startObserving()
    }

    @objc private func togglePopover() {
        if let popover, popover.isShown {
            popover.performClose(nil)
        } else if let button = statusItem.button {
            popover.show(relativeTo: button.bounds, of: button, preferredEdge: .minY)
            NSApp.activate(ignoringOtherApps: true)
        }
    }

    private func startObserving() {
        observeTask = Task { [weak self] in
            while !Task.isCancelled {
                guard let self else { return }
                let status = self.store.aggregateStatus
                if status != self.lastStatus {
                    self.lastStatus = status
                    self.updateIcon(status)
                }
                try? await Task.sleep(nanoseconds: 250_000_000)
            }
        }
    }

    private func updateIcon(_ status: ServiceStore.AggregateStatus) {
        let image: NSImage = switch status {
        case .ok: MenuBarIcon.ok()
        case .warning: MenuBarIcon.warning()
        case .error: MenuBarIcon.error()
        case .disconnected: MenuBarIcon.disconnected()
        }
        statusItem.button?.image = image
    }
}

struct PopoverContentView: View {
    let store: ServiceStore

    var body: some View {
        VStack(spacing: 0) {
            // Header
            HStack(alignment: .firstTextBaseline) {
                Text("AURELIA")
                    .font(.system(.title3, design: .default, weight: .black))
                    .foregroundStyle(LaminaTheme.fg)
                    .tracking(2)
                Spacer()

                if store.hasPeers {
                    ClusterToggle(isCluster: store.clusterMode) {
                        store.toggleClusterMode()
                    }
                }

                Text("CONSOLE")
                    .font(LaminaTheme.label)
                    .foregroundStyle(LaminaTheme.accent)
                    .tracking(3)
            }
            .padding(.horizontal, 16)
            .padding(.top, 14)
            .padding(.bottom, 10)

            Divider()
                .overlay(LaminaTheme.border)

            ServiceListView(store: store)

            Divider()
                .overlay(LaminaTheme.border)

            Button {
                NSApplication.shared.terminate(nil)
            } label: {
                Text("QUIT")
                    .font(LaminaTheme.monoSmall)
                    .foregroundStyle(LaminaTheme.muted)
                    .tracking(1)
                    .frame(maxWidth: .infinity)
            }
            .buttonStyle(.plain)
            .keyboardShortcut("q")
            .padding(.vertical, 10)
        }
        .frame(width: 400, height: 460)
        .background(LaminaTheme.bg)
    }
}
