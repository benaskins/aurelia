import SwiftUI

@main
struct AureliaConsoleApp: App {
    @State private var store = ServiceStore()

    var body: some Scene {
        MenuBarExtra {
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
            .onAppear { store.startPolling() }
        } label: {
            Image(nsImage: statusImage)
        }
        .menuBarExtraStyle(.window)
    }

    private var statusImage: NSImage {
        switch store.aggregateStatus {
        case .ok: MenuBarIcon.ok()
        case .warning: MenuBarIcon.warning()
        case .error: MenuBarIcon.error()
        case .disconnected: MenuBarIcon.disconnected()
        }
    }
}
