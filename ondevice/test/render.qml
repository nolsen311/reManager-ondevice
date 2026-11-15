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

    function shoot(name, then) {
        loader.item.grabToImage(function (result) {
            result.saveToFile(outDir + "/" + name + ".png")
            then()
        })
    }

    readonly property var steps: [
        function (next) { shoot("1-list", next) },
        function (next) { loader.item.installedOnly = true; loader.item.loadList(0); next() },
        function (next) { shoot("2-installed", next) },
        function (next) { loader.item.openDetail("retaskable"); next() },
        function (next) { shoot("3-detail", next) },
        function (next) { loader.item.openPlan("retaskable"); next() },
        function (next) { shoot("4-plan", next) },
        function (next) { loader.item.startInstall(); next() },
        function (next) { shoot("5-progress", next) },
        function (next) { endpoint().finish({ success: true, deferred: ["Build hashtable"] }); next() },
        function (next) { shoot("6-done", next) },
        function (next) { endpoint().finish({ success: false, errors: ["Installation failed: could not reach packages.vellum.delivery. Check that Wi-Fi is on."] }); next() },
        function (next) { shoot("7-failed", next) },
        function (next) { endpoint().scenario = "blocked"; loader.item.openPlan("bluetooth-settings"); next() },
        function (next) { shoot("8-blocked", next) },
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
