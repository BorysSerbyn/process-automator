import Foundation

/// Decoding the files the Go core writes (run status, notifications).
enum CoreJSON {
    /// A decoder for the core's JSON: dates are RFC 3339, with or without
    /// fractional seconds (Go writes up to nanoseconds).
    static func decoder() -> JSONDecoder {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .custom { decoder in
            let text = try decoder.singleValueContainer().decode(String.self)
            guard let date = parseDate(text) else {
                throw DecodingError.dataCorrupted(
                    .init(codingPath: decoder.codingPath, debugDescription: "not an RFC 3339 date: \(text)"))
            }
            return date
        }
        return decoder
    }

    static func parseDate(_ text: String) -> Date? {
        let fractional = ISO8601DateFormatter()
        fractional.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return fractional.date(from: text) ?? ISO8601DateFormatter().date(from: text)
    }
}
