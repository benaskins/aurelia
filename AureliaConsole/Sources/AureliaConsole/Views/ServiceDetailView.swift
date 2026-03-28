import SwiftUI

struct ServiceDetailView: View {
    let service: ServiceInfo
    @State private var logLines: [String] = []
    @State private var logTask: Task<Void, Never>?
    let store: ServiceStore

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            // Service metadata
            HStack(spacing: 14) {
                if let pid = service.pid {
                    metadataLabel("PID", "\(pid)")
                }
                if let uptime = service.uptime {
                    metadataLabel("UP", uptime)
                }
                if service.restartCount > 0 {
                    metadataLabel("RST", "\(service.restartCount)")
                }
            }

            if let lastError = service.lastError, !lastError.isEmpty {
                Text(lastError)
                    .font(LaminaTheme.monoTiny)
                    .foregroundStyle(LaminaTheme.statusError)
            }

            // Log tail
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 0) {
                        ForEach(Array(logLines.enumerated()), id: \.offset) { index, line in
                            Text(line)
                                .font(.system(size: 10, design: .monospaced))
                                .foregroundStyle(LaminaTheme.muted)
                                .textSelection(.enabled)
                                .id(index)
                                .padding(.vertical, 0.5)
                        }
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(8)
                }
                .frame(height: 150)
                .background(Color.black.opacity(0.4))
                .clipShape(RoundedRectangle(cornerRadius: 3))
                .overlay(
                    RoundedRectangle(cornerRadius: 3)
                        .stroke(LaminaTheme.border, lineWidth: 1)
                )
                .onChange(of: logLines.count) {
                    if let last = logLines.indices.last {
                        proxy.scrollTo(last, anchor: .bottom)
                    }
                }
            }
        }
        .padding(.horizontal, 14)
        .padding(.bottom, 8)
        .background(LaminaTheme.panelBg)
        .onAppear { startLogPolling() }
        .onDisappear { stopLogPolling() }
    }

    private func metadataLabel(_ key: String, _ value: String) -> some View {
        HStack(spacing: 4) {
            Text(key)
                .font(.system(size: 9, weight: .bold, design: .monospaced))
                .foregroundStyle(LaminaTheme.accent)
                .tracking(1)
            Text(value)
                .font(LaminaTheme.monoTiny)
                .foregroundStyle(LaminaTheme.muted)
        }
    }

    private func startLogPolling() {
        logTask = Task {
            while !Task.isCancelled {
                let raw = await store.logs(service: service.name)
                logLines = raw.map { stripANSI($0) }
                try? await Task.sleep(nanoseconds: 2_000_000_000)
            }
        }
    }

    private func stopLogPolling() {
        logTask?.cancel()
        logTask = nil
    }

    private func stripANSI(_ string: String) -> String {
        string.replacingOccurrences(
            of: "\u{1B}\\[[0-9;]*m",
            with: "",
            options: .regularExpression
        )
    }
}
