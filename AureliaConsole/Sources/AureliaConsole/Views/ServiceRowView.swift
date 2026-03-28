import SwiftUI

struct ServiceRowView: View {
    let service: ServiceInfo
    let isExpanded: Bool
    let onToggle: () -> Void
    let onAction: (String) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 8) {
                Circle()
                    .fill(statusColor)
                    .frame(width: 8, height: 8)

                Text(service.name)
                    .font(.system(.body, design: .monospaced, weight: .medium))

                if let port = service.port {
                    Text(":\(port)")
                        .font(.system(.caption, design: .monospaced))
                        .foregroundStyle(.secondary)
                }

                Spacer()

                Text(service.type)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .padding(.horizontal, 5)
                    .padding(.vertical, 2)
                    .background(.quaternary)
                    .clipShape(RoundedRectangle(cornerRadius: 3))

                actionButtons
            }
            .contentShape(Rectangle())
            .onTapGesture(perform: onToggle)
        }
        .padding(.vertical, 4)
    }

    @ViewBuilder
    private var actionButtons: some View {
        switch service.state {
        case .stopped, .failed:
            Button { onAction("start") } label: {
                Image(systemName: "play.fill")
                    .font(.caption)
            }
            .buttonStyle(.borderless)
        case .running:
            HStack(spacing: 4) {
                Button { onAction("restart") } label: {
                    Image(systemName: "arrow.clockwise")
                        .font(.caption)
                }
                .buttonStyle(.borderless)
                Button { onAction("stop") } label: {
                    Image(systemName: "stop.fill")
                        .font(.caption)
                }
                .buttonStyle(.borderless)
            }
        case .starting, .stopping:
            ProgressView()
                .controlSize(.small)
        }
    }

    private var statusColor: Color {
        switch (service.state, service.health) {
        case (.failed, _): .red
        case (.running, .healthy): .green
        case (.running, .unhealthy): .orange
        case (.starting, _), (.stopping, _): .yellow
        case (.stopped, _): .gray
        default: .gray
        }
    }
}
