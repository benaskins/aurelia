import Foundation

/// Performs HTTP requests over a Unix domain socket via curl.
struct UnixSocketHTTP: Sendable {
    let socketPath: String

    func request(method: String, path: String) async throws -> (Int, Data) {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/usr/bin/curl")
        process.arguments = [
            "--unix-socket", socketPath,
            "-s",
            "-w", "\n%{http_code}",
            "-X", method,
            "http://localhost\(path)",
        ]

        let stdout = Pipe()
        let stderr = Pipe()
        process.standardOutput = stdout
        process.standardError = stderr

        try process.run()
        process.waitUntilExit()

        let output = stdout.fileHandleForReading.readDataToEndOfFile()

        if process.terminationStatus != 0 {
            throw HTTPError.connectionFailed
        }

        // Last line is the HTTP status code (from -w flag)
        guard let str = String(data: output, encoding: .utf8),
              let lastNewline = str.lastIndex(of: "\n"),
              let statusCode = Int(str[str.index(after: lastNewline)...]) else {
            throw HTTPError.invalidResponse
        }

        let bodyEnd = str.index(before: lastNewline)
        let body = Data(str[str.startIndex...bodyEnd].utf8)

        return (statusCode, body)
    }

    enum HTTPError: Error, LocalizedError {
        case connectionFailed
        case invalidResponse

        var errorDescription: String? {
            switch self {
            case .connectionFailed: "Cannot connect to aurelia daemon"
            case .invalidResponse: "Invalid response from daemon"
            }
        }
    }
}
