import Foundation

@MainActor
@Observable
final class ServiceStore {
    var services: [ServiceInfo] = []
    var peers: [String: String] = [:]  // node name -> "ok" | "timeout" | "error" | "unreachable"
    var isConnected = false
    var clusterMode = false
    var hasPeers = false
    var error: String?

    private let client = AureliaClient()
    private var pollTask: Task<Void, Never>?
    private var backoff = false
    private var consecutiveFailures = 0

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

    func toggleClusterMode() {
        clusterMode.toggle()
    }

    private func poll() async {
        do {
            if clusterMode {
                let response = try await client.clusterServices()
                services = response.services.sorted {
                    if $0.node != $1.node { return ($0.node ?? "") < ($1.node ?? "") }
                    return $0.name < $1.name
                }
                peers = response.peers
                hasPeers = !response.peers.isEmpty
            } else {
                let result = try await client.services()
                services = result.sorted(by: { $0.name < $1.name })
                // Check for peers once so the toggle appears
                if !hasPeers {
                    if let response = try? await client.clusterServices() {
                        hasPeers = !response.peers.isEmpty
                        peers = response.peers
                    }
                }
            }
            isConnected = true
            error = nil
            backoff = false
            consecutiveFailures = 0
        } catch {
            consecutiveFailures += 1
            if consecutiveFailures >= 3 {
                isConnected = false
                services = []
                peers = [:]
                self.error = error.localizedDescription
                backoff = true
            }
        }
    }

    // MARK: - Service actions

    func start(service: ServiceInfo) async {
        await performAction(service: service, action: "start")
    }

    func stop(service: ServiceInfo) async {
        await performAction(service: service, action: "stop")
    }

    func restart(service: ServiceInfo) async {
        await performAction(service: service, action: "restart")
    }

    func logs(service: String, node: String? = nil) async -> [String] {
        do {
            if clusterMode, let node {
                return try await client.clusterLogs(service: service, node: node)
            }
            return try await client.logs(service: service)
        } catch {
            return ["Error fetching logs: \(error.localizedDescription)"]
        }
    }

    private func performAction(service: ServiceInfo, action: String) async {
        do {
            if clusterMode, let node = service.node {
                try await client.clusterAction(service: service.name, action: action, node: node)
            } else {
                try await client.action(service: service.name, action: action)
            }
            await poll()
        } catch {
            self.error = error.localizedDescription
        }
    }

    // MARK: - Computed

    /// Unique node names from the current service list, in order.
    var nodeNames: [String] {
        var seen = Set<String>()
        var result: [String] = []
        for service in services {
            let node = service.node ?? "local"
            if seen.insert(node).inserted {
                result.append(node)
            }
        }
        return result
    }

    /// Services grouped by node.
    func services(forNode node: String) -> [ServiceInfo] {
        services.filter { ($0.node ?? "local") == node }
    }

    enum AggregateStatus: Hashable {
        case ok, warning, error, disconnected
    }

    var aggregateStatus: AggregateStatus {
        if !isConnected { return .disconnected }
        if services.isEmpty { return .disconnected }
        if services.contains(where: { $0.state == .failed }) { return .error }
        if services.contains(where: {
            $0.state == .starting || $0.state == .stopping || $0.health == .unhealthy
        }) { return .warning }
        // In cluster mode, check for unhealthy peers
        if clusterMode && peers.values.contains(where: { $0 != "ok" }) { return .warning }
        return .ok
    }
}
