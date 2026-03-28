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
            Image(systemName: statusIcon)
                .symbolRenderingMode(.palette)
                .foregroundStyle(statusColor)
        }
        .menuBarExtraStyle(.window)
    }

    private var statusIcon: String {
        switch store.aggregateStatus {
        case .ok: "circle.fill"
        case .warning: "exclamationmark.circle.fill"
        case .error: "xmark.circle.fill"
        case .disconnected: "circle.dashed"
        }
    }

    private var statusColor: Color {
        switch store.aggregateStatus {
        case .ok: LaminaTheme.statusOk
        case .warning: LaminaTheme.statusWarn
        case .error: LaminaTheme.statusError
        case .disconnected: LaminaTheme.statusOff
        }
    }
}
