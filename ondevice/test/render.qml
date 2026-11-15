import QtQuick 2.15
import QtQuick.Window 2.15

// Renders each screen of the UI to a PNG with the mock endpoint, so the layout can be looked at without a tablet.
// Run through ondevice/test/render.sh.
Window {
    id: window
    width: 954
    height: 1696
    visible: true

    readonly property string outDir: Qt.application.arguments[Qt.application.arguments.length - 1]
    property int step: 0

    Loader {
        id: loader
        anchors.fill: parent
        source: "../ui/Main.qml"
    }

    function endpoint() {
        // The AppLoad endpoint is the root's first non-visual child of the mock's type.
        for (var i = 0; i < loader.item.children.length; i++)
            if (loader.item.children[i].scenario !== undefined)
                return loader.item.children[i]
        return null
    }

    // Long enough for the screen's probes to get past the dropped ones.
    function wait(then) {
        waitTimer.then = then
        waitTimer.start()
    }

    Timer {
        id: waitTimer
        property var then
        interval: 2500
        onTriggered: then()
    }

    function shoot(name, then) {
        loader.item.grabToImage(function (result) {
            result.saveToFile(outDir + "/" + name + ".png")
            then()
        })
    }

    readonly property var steps: [
        function (next) { wait(next) },
        function (next) { shoot("01-list", next) },
        function (next) { loader.item.showIncompatible = true; loader.item.loadList(0); next() },
        function (next) { shoot("01b-list-all-os", next) },
        function (next) { loader.item.showIncompatible = false; next() },
        function (next) { loader.item.showTab("installed"); next() },
        function (next) { shoot("02-installed", next) },
        function (next) { loader.item.openDetail("chessmarkable"); next() },
        function (next) { shoot("03-detail", next) },
        function (next) { loader.item.openPlan("plan", ["retaskable"]); next() },
        function (next) { shoot("04-plan-install", next) },
        function (next) { loader.item.startJob(); next() },
        function (next) { shoot("05-progress", next) },
        function (next) { endpoint().finish({ success: true, deferred: ["Build hashtable"] }); next() },
        function (next) { shoot("06-done", next) },
        function (next) { endpoint().finish({ success: false, errors: ["Installation failed: could not reach packages.vellum.delivery. Check that Wi-Fi is on."] }); next() },
        function (next) { shoot("07-failed", next) },
        function (next) { endpoint().scenario = "blocked"; loader.item.openPlan("plan", ["bluetooth-settings"]); next() },
        function (next) { shoot("08-blocked", next) },
        function (next) { loader.item.openDetail("koreader"); next() },
        function (next) { shoot("09-detail-installed", next) },
        function (next) { loader.item.openPlan("planRemove", ["koreader"]); next() },
        function (next) { shoot("10-plan-remove", next) },
        function (next) { loader.item.openDetail("appload"); next() },
        function (next) { shoot("11-detail-protected", next) },
        function (next) { loader.item.showTab("updates"); next() },
        function (next) { shoot("12-updates", next) },
        function (next) { loader.item.openPlan("planUpgrade", []); next() },
        function (next) { shoot("13-plan-upgrade", next) },
        function (next) { loader.item.startJob(); next() },
        function (next) { endpoint().finish({ success: true }); next() },
        function (next) { shoot("14-upgraded", next) },
        function (next) { loader.item.showTab("system"); next() },
        function (next) { shoot("15-system", next) },
        function (next) { loader.item.confirmSystem("rebuild-hashtable"); next() },
        function (next) { shoot("16-confirm-rebuild", next) },
        function (next) { loader.item.startSystem(); next() },
        function (next) { shoot("17-rebuilding", next) },
        function (next) { loader.item.showTab("all"); loader.item.openPlan("plan", ["retaskable"]); next() },
        function (next) { loader.item.startJob(); endpoint().finish({ success: true, deferred: ["Build hashtable"] }); next() },
        function (next) { shoot("18-installed-needs-rebuild", next) },
        function (next) { loader.item.showTab("all"); next() },
        function (next) { loader.item.openSearch(); loader.item.editDraft("xovi"); next() },
        function (next) { wait(next) },
        function (next) { shoot("19-search", next) },
        function (next) { loader.item.applySearch(loader.item.draftQuery); next() },
        function (next) { shoot("20-search-results", next) },
        function (next) { Qt.quit() }
    ]

    Timer {
        interval: 300
        running: true
        repeat: true
        onTriggered: {
            if (window.step < window.steps.length) {
                var run = window.steps[window.step++]
                stop()
                run(function () { start() })
            }
        }
    }
}
