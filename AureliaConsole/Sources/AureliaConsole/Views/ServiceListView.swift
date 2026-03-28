import SwiftUI

struct ServiceListView: View {
    let store: ServiceStore
    @State private var expandedService: String?

    var body: some View {
        if !store.isConnected {
            DisconnectedView()
        } else if store.services.isEmpty {
            Text("No services")
                .foregroundStyle(.secondary)
                .frame(maxWidth: .infinity, alignment: .center)
                .padding()
        } else {
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 0) {
                    ForEach(store.services) { service in
                        VStack(alignment: .leading, spacing: 0) {
                            ServiceRowView(
                                service: service,
                                isExpanded: expandedService == service.name,
                                onToggle: {
                                    withAnimation(.easeInOut(duration: 0.2)) {
                                        expandedService = expandedService == service.name ? nil : service.name
                                    }
                                },
                                onAction: { action in
                                    Task {
                                        switch action {
                                        case "start": await store.start(service: service.name)
                                        case "stop": await store.stop(service: service.name)
                                        case "restart": await store.restart(service: service.name)
                                        default: break
                                        }
                                    }
                                }
                            )

                            if expandedService == service.name {
                                ServiceDetailView(service: service, store: store)
                            }

                            Divider()
                        }
                    }
                }
                .padding(.horizontal, 12)
            }
        }
    }
}

struct DisconnectedView: View {
    var body: some View {
        VStack(spacing: 8) {
            Image(systemName: "bolt.horizontal.circle")
                .font(.title2)
                .foregroundStyle(.secondary)
            Text("Aurelia not running")
                .font(.headline)
                .foregroundStyle(.secondary)
            Text("Waiting for daemon...")
                .font(.caption)
                .foregroundStyle(.tertiary)
        }
        .frame(maxWidth: .infinity, alignment: .center)
        .padding()
    }
}
