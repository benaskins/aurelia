import SwiftUI

struct ServiceDetailView: View {
    let service: ServiceInfo
    @State private var logLines: [String] = []
    @State private var logTask: Task<Void, Never>?
    let store: ServiceStore

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            // Service metadata
            HStack(spacing: 12) {
                if let pid = service.pid {
                    Label("PID \(pid)", systemImage: "number")
                }
                if let uptime = service.uptime {
                    Label(uptime, systemImage: "clock")
                }
                if service.restartCount > 0 {
                    Label("\(service.restartCount)x", systemImage: "arrow.counterclockwise")
                }
            }
            .font(.caption)
            .foregroundStyle(.secondary)

            if let lastError = service.lastError, !lastError.isEmpty {
                Text(lastError)
                    .font(.caption)
                    .foregroundStyle(.red)
            }

            // Log tail
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 1) {
                        ForEach(Array(logLines.enumerated()), id: \.offset) { index, line in
                            Text(line)
                                .font(.system(.caption2, design: .monospaced))
                                .foregroundStyle(.secondary)
                                .textSelection(.enabled)
                                .id(index)
                        }
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                }
                .frame(height: 150)
                .background(.black.opacity(0.05))
                .clipShape(RoundedRectangle(cornerRadius: 4))
                .onChange(of: logLines.count) {
                    if let last = logLines.indices.last {
                        proxy.scrollTo(last, anchor: .bottom)
                    }
                }
            }
        }
        .padding(.leading, 16)
        .padding(.vertical, 4)
        .onAppear { startLogPolling() }
        .onDisappear { stopLogPolling() }
    }

    private func startLogPolling() {
        logTask = Task {
            while !Task.isCancelled {
                logLines = await store.logs(service: service.name)
                try? await Task.sleep(nanoseconds: 2_000_000_000)
            }
        }
    }

    private func stopLogPolling() {
        logTask?.cancel()
        logTask = nil
    }
}
