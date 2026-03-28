import Foundation

@MainActor
@Observable
final class GraphStore {
    var graphResponse: ClusterGraphResponse?
    var selectedNode: String?
    var showAllServices = false
    var highlightedService: String?

    private let client = AureliaClient()
    private var pollTask: Task<Void, Never>?

    var availableNodes: [String] {
        guard let nodes = graphResponse?.nodes else { return [] }
        var seen = Set<String>()
        var result: [String] = []
        for n in nodes {
            let node = n.node ?? "local"
            if seen.insert(node).inserted {
                result.append(node)
            }
        }
        return result
    }

    var layout: GraphLayout {
        computeLayout()
    }

    func startPolling() {
        guard pollTask == nil else { return }
        pollTask = Task { [weak self] in
            while !Task.isCancelled {
                guard let self else { return }
                await self.refresh()
                try? await Task.sleep(nanoseconds: 5_000_000_000)
            }
        }
    }

    func stopPolling() {
        pollTask?.cancel()
        pollTask = nil
    }

    func refresh() async {
        do {
            graphResponse = try await client.clusterGraph()
            if selectedNode == nil {
                // Default to the node with the most dependency edges
                selectedNode = bestDefaultNode()
            }
        } catch {
            // Silently retry on next poll
        }
    }

    /// Returns the set of services transitively depended on by the given service.
    func transitiveDeps(of service: String) -> Set<String> {
        guard let nodes = graphResponse?.nodes, let selected = selectedNode else { return [] }
        // Filter to selected node to avoid duplicate name collisions across nodes
        var nodeMap: [String: GraphNode] = [:]
        for n in nodes where (n.node ?? "local") == selected {
            nodeMap[n.name] = n
        }
        var result = Set<String>()
        var queue = [service]
        while !queue.isEmpty {
            let current = queue.removeFirst()
            guard let node = nodeMap[current] else { continue }
            for dep in (node.requires ?? []) + (node.after ?? []) {
                if result.insert(dep).inserted {
                    queue.append(dep)
                }
            }
        }
        return result
    }

    private func bestDefaultNode() -> String? {
        guard let nodes = graphResponse?.nodes else { return availableNodes.first }
        var edgeCount: [String: Int] = [:]
        for n in nodes {
            let node = n.node ?? "local"
            let deps = (n.requires ?? []).count + (n.after ?? []).count
            edgeCount[node, default: 0] += deps
        }
        return edgeCount.max(by: { $0.value < $1.value })?.key ?? availableNodes.first
    }

    // MARK: - Layout Algorithm

    private func computeLayout() -> GraphLayout {
        guard let response = graphResponse, let selected = selectedNode else {
            return GraphLayout(nodes: [], edges: [], canvasSize: .zero)
        }

        // Filter to selected node
        var nodesByName: [String: GraphNode] = [:]
        for n in response.nodes where (n.node ?? "local") == selected {
            nodesByName[n.name] = n
        }

        // If not showing all, keep only services that participate in dependencies
        let filteredNames: Set<String>
        if showAllServices {
            filteredNames = Set(nodesByName.keys)
        } else {
            var connected = Set<String>()
            for (name, node) in nodesByName {
                let deps = (node.requires ?? []) + (node.after ?? [])
                if !deps.isEmpty {
                    connected.insert(name)
                    for dep in deps where nodesByName[dep] != nil {
                        connected.insert(dep)
                    }
                }
            }
            filteredNames = connected
        }

        guard !filteredNames.isEmpty else {
            return GraphLayout(nodes: [], edges: [], canvasSize: .zero)
        }

        // Assign layers via topological sort
        var layers: [String: Int] = [:]
        func assignLayer(_ name: String) -> Int {
            if let cached = layers[name] { return cached }
            guard let node = nodesByName[name] else { return 0 }
            let deps = ((node.requires ?? []) + (node.after ?? []))
                .filter { filteredNames.contains($0) }
            if deps.isEmpty {
                layers[name] = 0
                return 0
            }
            // Guard against cycles
            layers[name] = 0
            let maxDep = deps.map { assignLayer($0) }.max() ?? 0
            let layer = maxDep + 1
            layers[name] = layer
            return layer
        }

        for name in filteredNames {
            _ = assignLayer(name)
        }

        // Group by layer, sort within layer
        var layerGroups: [Int: [String]] = [:]
        for name in filteredNames {
            let layer = layers[name] ?? 0
            layerGroups[layer, default: []].append(name)
        }
        for key in layerGroups.keys {
            layerGroups[key]?.sort()
        }

        // Build LayoutNodes
        var layoutNodes: [LayoutNode] = []
        var nodePositions: [String: LayoutNode] = [:]
        for (layer, names) in layerGroups.sorted(by: { $0.key < $1.key }) {
            for (pos, name) in names.enumerated() {
                guard let gn = nodesByName[name] else { continue }
                let ln = LayoutNode(
                    name: name, type: gn.type, state: gn.state, health: gn.health,
                    layer: layer, positionInLayer: pos
                )
                layoutNodes.append(ln)
                nodePositions[name] = ln
            }
        }

        // Build LayoutEdges
        var layoutEdges: [LayoutEdge] = []
        for name in filteredNames {
            guard let gn = nodesByName[name], let toNode = nodePositions[name] else { continue }
            for dep in (gn.requires ?? []) {
                guard let fromNode = nodePositions[dep] else { continue }
                layoutEdges.append(LayoutEdge(
                    from: dep, to: name, kind: .requires,
                    fromPoint: CGPoint(x: fromNode.frame.maxX, y: fromNode.frame.midY),
                    toPoint: CGPoint(x: toNode.frame.minX, y: toNode.frame.midY)
                ))
            }
            for dep in (gn.after ?? []) where !(gn.requires ?? []).contains(dep) {
                guard let fromNode = nodePositions[dep] else { continue }
                layoutEdges.append(LayoutEdge(
                    from: dep, to: name, kind: .after,
                    fromPoint: CGPoint(x: fromNode.frame.maxX, y: fromNode.frame.midY),
                    toPoint: CGPoint(x: toNode.frame.minX, y: toNode.frame.midY)
                ))
            }
        }

        // Canvas size
        let maxX = layoutNodes.map(\.frame.maxX).max() ?? 0
        let maxY = layoutNodes.map(\.frame.maxY).max() ?? 0
        let canvasSize = CGSize(
            width: maxX + GraphLayout.margin,
            height: maxY + GraphLayout.margin
        )

        return GraphLayout(nodes: layoutNodes, edges: layoutEdges, canvasSize: canvasSize)
    }
}
