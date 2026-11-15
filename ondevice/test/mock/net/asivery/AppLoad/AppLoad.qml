import QtQuick 2.15

// Stands in for AppLoad's endpoint when the UI is rendered on a PC. It answers with canned data shaped like internal/ondevice's replies; it does not run the backend.
Item {
    id: mock
    property string applicationID
    signal messageReceived(int type, string contents)

    // Set by the harness: "ok", "blocked" or "running".
    property string scenario: "ok"

    function terminate() {}

    readonly property var catalogue: [
        { name: "appload", version: "0.5.3-r3", description: "XOVI extension for windowed and fullscreen apps", category: "launchers", installed: true, installedVersion: "0.5.3-r3", compatible: true },
        { name: "battery-percentage", version: "1.0.0-r0", description: "Shows the battery percentage next to the battery icon in the main view header.", category: "ui", installed: true, installedVersion: "1.0.0-r0", compatible: true },
        { name: "bluetooth-settings", version: "1.2.0-r1", description: "Adds a Bluetooth page to settings", category: "ui", installed: false, compatible: false },
        { name: "chessmarkable", version: "0.9.1-r0", description: "Chess for reMarkable tablets", category: "apps", installed: false, compatible: true },
        { name: "koreader", version: "2026.07.1-r1", description: "An ebook reader application supporting PDF, DjVu, EPUB, FB2 and many more formats.", category: "apps", installed: true, installedVersion: "2026.07.1-r1", compatible: true },
        { name: "qt-resource-rebuilder", version: "19.0.1-r0", description: "Rebuilds QT resource databases on the fly to replace or add QML files", category: "framework", installed: true, installedVersion: "19.0.1-r0", compatible: true },
        { name: "random-sleep-screen", version: "1.0.7-r3", description: "Picks random sleep screen from ~/sleepScreens/ folder.", category: "ui", installed: true, installedVersion: "1.0.7-r3", compatible: true },
        { name: "retaskable", version: "1.0.5-r1", description: "CalDAV task client for reMarkable tablets", category: "utilities", installed: false, compatible: true },
        { name: "tripletap", version: "1.1.1-r0", description: "Start xovi by triple-pressing the power button", category: "utilities", installed: true, installedVersion: "1.1.1-r0", compatible: true },
        { name: "xovi", version: "0.3.3-r2", description: "Core xovi framework for reMarkable tablets", category: "framework", installed: true, installedVersion: "0.3.3-r2", compatible: true },
        { name: "xovi-extensions", version: "19.0.1-r0", description: "Extensions for xovi framework", category: "framework", installed: true, installedVersion: "19.0.1-r0", compatible: true }
    ]

    function reply(id, result) {
        messageReceived(2, JSON.stringify({ id: id, result: result }))
    }

    function event(e) {
        messageReceived(3, JSON.stringify(e))
    }

    function sendMessage(type, contents) {
        var req = JSON.parse(contents)
        var p = req.params || {}
        switch (req.method) {
        case "device":
            reply(req.id, { model: "rmppmove", name: "reMarkable Paper Pro Move", firmware: "3.27.3.0", tested: true })
            break
        case "job":
            reply(req.id, scenario === "running"
                ? { running: true, events: [
                    { seq: 1, kind: "started", action: "install", packages: ["retaskable"] },
                    { seq: 2, kind: "progress", progress: { package: "retaskable", index: 0, total: 1, status: "installing", message: "Installing retaskable (1/1)…" } } ] }
                : { running: false, events: [] })
            break
        case "list":
            var all = catalogue.filter(function (pkg) { return !p.installedOnly || pkg.installed })
            reply(req.id, { packages: all.slice(p.offset, p.offset + p.limit), total: all.length, offset: p.offset })
            break
        case "detail":
            var found = catalogue.filter(function (pkg) { return pkg.name === p.name })[0]
            reply(req.id, Object.assign({ author: "jdkruzr", license: "GPL-3.0-only", url: "https://example.org", depends: ["appload"], protected: p.name === "appload" }, found))
            break
        case "plan":
            reply(req.id, scenario === "blocked"
                ? { action: "install", requested: p.names, packages: [], error: "ERROR: unable to select packages:\n  bluetooth-settings-1.2.0-r1:\n    breaks: world[remarkable-os<3.27]\n    satisfies: world[bluetooth-settings]" }
                : { action: "install", requested: p.names, packages: ["appload", "retaskable"], upgrades: { appload: "0.5.3-r2 -> 0.5.3-r3" } })
            break
        case "planRemove":
            reply(req.id, { action: "remove", requested: p.names, packages: p.names })
            break
        case "upgrades":
            reply(req.id, [
                { name: "appload", from: "0.5.3-r2", to: "0.5.3-r3" },
                { name: "koreader", from: "2026.06.2-r0", to: "2026.07.1-r1" },
                { name: "xovi", from: "0.3.2-r0", to: "0.3.3-r2" } ])
            break
        case "planUpgrade":
            reply(req.id, { action: "upgrade", requested: p.names, packages: ["appload", "koreader", "xovi"],
                upgrades: { appload: "0.5.3-r2 -> 0.5.3-r3", koreader: "2026.06.2-r0 -> 2026.07.1-r1", xovi: "0.3.2-r0 -> 0.3.3-r2" } })
            break
        case "install":
        case "remove":
        case "upgrade":
            reply(req.id, {})
            event({ seq: 1, kind: "started", action: req.method, packages: p.names })
            event({ seq: 2, kind: "progress", progress: { package: "retaskable", index: 1, total: 2, status: "installing", message: "Installing retaskable (2/2)…" } })
            break
        }
    }

    function finish(result) {
        event({ seq: 3, kind: "done", result: result })
    }

    Component.onCompleted: Qt.callLater(function () { mock.messageReceived(4, "") })
}
