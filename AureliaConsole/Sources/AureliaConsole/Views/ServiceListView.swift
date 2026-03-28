import SwiftUI

struct ServiceListView: View {
    let store: ServiceStore
    @State private var expandedService: String?

    var body: some View {
        if !store.isConnected {
            DisconnectedView()
        } else if store.services.isEmpty {
            Text("NO SERVICES")
                .font(LaminaTheme.label)
                .foregroundStyle(LaminaTheme.dim)
                .tracking(2)
                .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .center)
        } else {
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 0) {
                    ForEach(store.services) { service in
                        VStack(alignment: .leading, spacing: 0) {
                            ServiceRowView(
                                service: service,
                                isExpanded: expandedService == service.name,
                                onToggle: {
                                    withAnimation(.easeInOut(duration: 0.15)) {
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

                            Rectangle()
                                .fill(LaminaTheme.border)
                                .frame(height: 1)
                        }
                    }
                }
            }
        }
    }
}

struct DisconnectedView: View {
    var body: some View {
        VStack(spacing: 12) {
            Image(systemName: "bolt.horizontal.circle")
                .font(.system(size: 28, weight: .thin))
                .foregroundStyle(LaminaTheme.dim)
            Text("AURELIA NOT RUNNING")
                .font(LaminaTheme.label)
                .foregroundStyle(LaminaTheme.dim)
                .tracking(2)
            Text("waiting for daemon")
                .font(LaminaTheme.monoTiny)
                .foregroundStyle(LaminaTheme.dim)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .center)
    }
}
