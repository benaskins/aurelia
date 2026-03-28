import Foundation

@MainActor
@Observable
final class ServiceStore {
    var services: [ServiceInfo] = []
    var isConnected = false
    var error: String?

    private let client = AureliaClient()
    private var pollTask: Task<Void, Never>?
    private var backoff = false

    func startPolling() {
        guard pollTask == nil else { return }
        pollTask = Task { [weak self] in
            while !Task.isCancelled {
                guard let self else { return }
                await self.poll()
                let interval: UInt64 = self.backoff ? 5_000_000_000 : 1_000_000_000
                try? await Task.sleep(nanoseconds: interval)
            }
        }
    }

    func stopPolling() {
        pollTask?.cancel()
        pollTask = nil
    }

    private func poll() async {
        do {
            let result = try await client.services()
            services = result
            isConnected = true
            error = nil
            backoff = false
        } catch {
            isConnected = false
            services = []
            self.error = error.localizedDescription
            backoff = true
        }
    }

    // MARK: - Service actions

    func start(service: String) async {
        await performAction(service: service, action: "start")
    }

    func stop(service: String) async {
        await performAction(service: service, action: "stop")
    }

    func restart(service: String) async {
        await performAction(service: service, action: "restart")
    }

    func logs(service: String) async -> [String] {
        do {
            return try await client.logs(service: service)
        } catch {
            return ["Error fetching logs: \(error.localizedDescription)"]
        }
    }

    private func performAction(service: String, action: String) async {
        do {
            try await client.action(service: service, action: action)
            // Immediately re-poll to get updated state
            await poll()
        } catch {
            self.error = error.localizedDescription
        }
    }

    // MARK: - Aggregate status

    enum AggregateStatus {
        case ok, warning, error, disconnected
    }

    var aggregateStatus: AggregateStatus {
        if !isConnected { return .disconnected }
        if services.isEmpty { return .disconnected }
        if services.contains(where: { $0.state == .failed }) { return .error }
        if services.contains(where: {
            $0.state == .starting || $0.state == .stopping || $0.health == .unhealthy
        }) { return .warning }
        return .ok
    }
}
