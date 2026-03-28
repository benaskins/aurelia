import SwiftUI

struct ServiceRowView: View {
    let service: ServiceInfo
    let isExpanded: Bool
    let onToggle: () -> Void
    let onAction: (String) -> Void
    @State private var isHovered = false

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 10) {
                // Status dot
                Circle()
                    .fill(statusColor)
                    .frame(width: 7, height: 7)
                    .shadow(color: statusColor.opacity(0.5), radius: service.state == .running ? 3 : 0)

                // Name
                Text(service.name)
                    .font(LaminaTheme.mono)
                    .foregroundStyle(LaminaTheme.fg)

                // Port
                if let port = service.port {
                    Text(":\(port)")
                        .font(LaminaTheme.monoSmall)
                        .foregroundStyle(LaminaTheme.dim)
                }

                Spacer()

                // Type badge
                Text(service.type.uppercased())
                    .font(.system(size: 9, weight: .bold, design: .monospaced))
                    .foregroundStyle(LaminaTheme.accent)
                    .tracking(1.5)

                // Action buttons
                actionButtons
            }
            .padding(.vertical, 8)
            .padding(.horizontal, 14)
            .background(isHovered || isExpanded ? LaminaTheme.panelBgHover : .clear)
            .contentShape(Rectangle())
            .onTapGesture(perform: onToggle)
            .onHover { isHovered = $0 }
        }
    }

    @ViewBuilder
    private var actionButtons: some View {
        switch service.state {
        case .stopped, .failed:
            ActionButton(icon: "play.fill") { onAction("start") }
        case .running:
            HStack(spacing: 2) {
                ActionButton(icon: "arrow.clockwise") { onAction("restart") }
                ActionButton(icon: "stop.fill") { onAction("stop") }
            }
        case .starting, .stopping:
            ProgressView()
                .controlSize(.small)
                .tint(LaminaTheme.muted)
        }
    }

    private var statusColor: Color {
        switch (service.state, service.health) {
        case (.failed, _): LaminaTheme.statusError
        case (.running, .healthy): LaminaTheme.statusOk
        case (.running, .unhealthy): LaminaTheme.statusWarn
        case (.starting, _), (.stopping, _): LaminaTheme.statusWarn
        case (.stopped, _): LaminaTheme.statusOff
        default: LaminaTheme.statusOff
        }
    }
}

private struct ActionButton: View {
    let icon: String
    let action: () -> Void
    @State private var isHovered = false

    var body: some View {
        Button(action: action) {
            Image(systemName: icon)
                .font(.system(size: 10))
                .foregroundStyle(isHovered ? LaminaTheme.fg : LaminaTheme.muted)
                .frame(width: 22, height: 22)
        }
        .buttonStyle(.plain)
        .onHover { isHovered = $0 }
    }
}
