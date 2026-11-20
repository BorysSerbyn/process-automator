/// Settings grouped by their dotted keys, two levels deep:
/// `llm.budget.window` sits in llm › budget as "window", `llm.backend` in llm
/// as "backend". Groups and entries keep the order the core lists them in;
/// in a group, its own entries come before its subgroups.
public enum KeyTree {
    public struct Entry<Item> {
        /// The key without its groups' names.
        public let label: String
        public let item: Item
    }

    public struct Group<Item>: Identifiable {
        /// The group's path ("llm", "llm.budget").
        public let id: String
        public let name: String
        public var entries: [Entry<Item>] = []
        public var groups: [Group<Item>] = []
    }

    public static func build<Item>(_ items: [Item], key: (Item) -> String) -> [Group<Item>] {
        var tree: [Group<Item>] = []
        for item in items {
            let parts = key(item).split(separator: ".").map(String.init)
            let top = parts.first ?? ""
            if !tree.contains(where: { $0.name == top }) { tree.append(Group(id: top, name: top)) }
            let i = tree.firstIndex { $0.name == top }!
            switch parts.count {
            case 0, 1:
                tree[i].entries.append(Entry(label: top, item: item))
            case 2:
                tree[i].entries.append(Entry(label: parts[1], item: item))
            default:
                let sub = parts[1]
                if !tree[i].groups.contains(where: { $0.name == sub }) {
                    tree[i].groups.append(Group(id: top + "." + sub, name: sub))
                }
                let j = tree[i].groups.firstIndex { $0.name == sub }!
                tree[i].groups[j].entries.append(Entry(label: parts[2...].joined(separator: "."), item: item))
            }
        }
        return tree
    }
}
