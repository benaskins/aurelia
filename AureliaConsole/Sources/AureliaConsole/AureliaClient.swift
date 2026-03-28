import Foundation

actor AureliaClient {
    private let http: UnixSocketHTTP

    init(socketPath: String? = nil) {
        let path = socketPath ?? FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent(".aurelia/aurelia.sock").path
        self.http = UnixSocketHTTP(socketPath: path)
    }

    func services() async throws -> [ServiceInfo] {
        let data = try await get("/v1/services")
        return try JSONDecoder().decode([ServiceInfo].self, from: data)
    }

    func logs(service: String, lines: Int = 50) async throws -> [String] {
        let data = try await get("/v1/services/\(service)/logs?n=\(lines)")
        let response = try JSONDecoder().decode(LogResponse.self, from: data)
        return response.lines ?? []
    }

    func action(service: String, action: String) async throws {
        let data = try await post("/v1/services/\(service)/\(action)")
        if let errorResponse = try? JSONDecoder().decode(ErrorResponse.self, from: data) {
            throw ClientError.apiError(errorResponse.error)
        }
    }

    func clusterServices() async throws -> ClusterServicesResponse {
        let data = try await get("/v1/cluster/services")
        return try JSONDecoder().decode(ClusterServicesResponse.self, from: data)
    }

    func clusterGraph() async throws -> ClusterGraphResponse {
        let data = try await get("/v1/cluster/graph")
        return try JSONDecoder().decode(ClusterGraphResponse.self, from: data)
    }

    func clusterAction(service: String, action: String, node: String) async throws {
        let data = try await post("/v1/cluster/services/\(service)/\(action)?node=\(node)")
        if let errorResponse = try? JSONDecoder().decode(ErrorResponse.self, from: data) {
            throw ClientError.apiError(errorResponse.error)
        }
    }

    func health() async throws -> Bool {
        let data = try await get("/v1/health")
        let json = try JSONDecoder().decode([String: String].self, from: data)
        return json["status"] == "ok"
    }

    // MARK: - HTTP

    private func get(_ path: String) async throws -> Data {
        let (status, data) = try await http.request(method: "GET", path: path)
        guard (200..<300).contains(status) else {
            if let errorResponse = try? JSONDecoder().decode(ErrorResponse.self, from: data) {
                throw ClientError.apiError(errorResponse.error)
            }
            throw ClientError.httpError(status)
        }
        return data
    }

    private func post(_ path: String) async throws -> Data {
        let (status, data) = try await http.request(method: "POST", path: path)
        guard (200..<300).contains(status) else {
            if let errorResponse = try? JSONDecoder().decode(ErrorResponse.self, from: data) {
                throw ClientError.apiError(errorResponse.error)
            }
            throw ClientError.httpError(status)
        }
        return data
    }

    enum ClientError: Error, LocalizedError {
        case httpError(Int)
        case apiError(String)

        var errorDescription: String? {
            switch self {
            case .httpError(let code): "HTTP \(code) from daemon"
            case .apiError(let message): message
            }
        }
    }
}
