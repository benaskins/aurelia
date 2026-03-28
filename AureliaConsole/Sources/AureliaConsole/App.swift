import SwiftUI

@main
struct AureliaConsoleApp: App {
    @State private var store = ServiceStore()

    var body: some Scene {
        MenuBarExtra {
            VStack(spacing: 0) {
                ServiceListView(store: store)

                Divider()

                Button("Quit AureliaConsole") {
                    NSApplication.shared.terminate(nil)
                }
                .keyboardShortcut("q")
                .padding(.vertical, 8)
                .padding(.horizontal, 12)
            }
            .frame(width: 380, height: 420)
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
        case .ok: .green
        case .warning: .yellow
        case .error: .red
        case .disconnected: .gray
        }
    }
}
