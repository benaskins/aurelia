import Foundation
import Network

/// Performs HTTP requests over a Unix domain socket using Network.framework.
struct UnixSocketHTTP: Sendable {
    let socketPath: String

    func request(method: String, path: String) async throws -> (Int, Data) {
        let connection = NWConnection(
            to: .unix(path: socketPath),
            using: .tcp
        )

        let box = ContinuationBox()

        return try await withCheckedThrowingContinuation { continuation in
            connection.stateUpdateHandler = { state in
                switch state {
                case .ready:
                    let httpRequest = "\(method) \(path) HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
                    connection.send(
                        content: Data(httpRequest.utf8),
                        contentContext: .finalMessage,
                        isComplete: true,
                        completion: .contentProcessed { error in
                            if let error {
                                connection.cancel()
                                box.resume(continuation, throwing: error)
                                return
                            }
                            // Read chunks until connection closes
                            let accumulator = DataAccumulator()
                            self.readChunks(
                                connection: connection,
                                accumulator: accumulator,
                                box: box,
                                continuation: continuation
                            )
                        }
                    )
                case .failed(let error):
                    connection.cancel()
                    box.resume(continuation, throwing: error)
                case .cancelled:
                    break
                default:
                    break
                }
            }
            connection.start(queue: .global())
        }
    }

    private func readChunks(
        connection: NWConnection,
        accumulator: DataAccumulator,
        box: ContinuationBox,
        continuation: CheckedContinuation<(Int, Data), any Error>
    ) {
        connection.receive(minimumIncompleteLength: 1, maximumLength: 65536) { data, _, isComplete, error in
            if let data {
                accumulator.append(data)
            }
            if isComplete || error != nil {
                connection.cancel()
                let allData = accumulator.data
                if allData.isEmpty {
                    box.resume(continuation, throwing: error ?? HTTPError.invalidResponse)
                } else {
                    do {
                        let result = try self.parseResponse(allData)
                        box.resume(continuation, returning: result)
                    } catch {
                        box.resume(continuation, throwing: error)
                    }
                }
                return
            }
            // Keep reading
            self.readChunks(
                connection: connection,
                accumulator: accumulator,
                box: box,
                continuation: continuation
            )
        }
    }

    private func parseResponse(_ data: Data) throws -> (Int, Data) {
        let bytes = Array(data)

        // Find \r\n\r\n header/body separator
        var headerEnd = -1
        for i in 0..<(bytes.count - 3) {
            if bytes[i] == 0x0D && bytes[i+1] == 0x0A && bytes[i+2] == 0x0D && bytes[i+3] == 0x0A {
                headerEnd = i
                break
            }
        }
        guard headerEnd >= 0 else { throw HTTPError.invalidResponse }

        let bodyData = Data(bytes[(headerEnd + 4)...])

        // Find first \r\n to extract status line
        var firstLineEnd = -1
        for i in 0..<headerEnd {
            if bytes[i] == 0x0D && bytes[i+1] == 0x0A {
                firstLineEnd = i
                break
            }
        }
        guard firstLineEnd >= 0 else { throw HTTPError.invalidResponse }

        guard let statusLine = String(bytes: bytes[0..<firstLineEnd], encoding: .utf8) else {
            throw HTTPError.invalidResponse
        }
        let parts = statusLine.split(separator: " ", maxSplits: 2)
        guard parts.count >= 2, let statusCode = Int(parts[1]) else {
            throw HTTPError.invalidResponse
        }

        return (statusCode, bodyData)
    }

    enum HTTPError: Error, LocalizedError {
        case invalidResponse

        var errorDescription: String? {
            switch self {
            case .invalidResponse: "Invalid HTTP response from daemon"
            }
        }
    }
}

/// Thread-safe accumulator for response data chunks.
private final class DataAccumulator: @unchecked Sendable {
    private var _data = Data()
    private let lock = NSLock()

    func append(_ chunk: Data) {
        lock.lock()
        _data.append(chunk)
        lock.unlock()
    }

    var data: Data {
        lock.lock()
        defer { lock.unlock() }
        return _data
    }
}

/// Guards against double-resuming a continuation when NWConnection fires
/// both a receive completion and a .failed state transition.
private final class ContinuationBox: @unchecked Sendable {
    private var resumed = false
    private let lock = NSLock()

    func resume(
        _ continuation: CheckedContinuation<(Int, Data), any Error>,
        returning value: (Int, Data)
    ) {
        lock.lock()
        defer { lock.unlock() }
        guard !resumed else { return }
        resumed = true
        continuation.resume(returning: value)
    }

    func resume(
        _ continuation: CheckedContinuation<(Int, Data), any Error>,
        throwing error: any Error
    ) {
        lock.lock()
        defer { lock.unlock() }
        guard !resumed else { return }
        resumed = true
        continuation.resume(throwing: error)
    }
}
