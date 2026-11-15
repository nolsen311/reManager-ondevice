import QtQuick 2.15
import net.asivery.AppLoad 1.0

// reManager's on-tablet frontend. It holds no package logic: every screen shows what the backend answered.
Rectangle {
    id: root
    anchors.fill: parent
    color: "white"

    // AppLoad looks for these two on the root item.
    signal close
    function unloading() {
        // An install runs in its own worker, so stopping the backend never interrupts one.
        endpoint.terminate()
    }

    // Sizes are in units of 1/954 of the width, the Paper Pro Move's portrait width, so a window or another screen scales the layout.
    readonly property real unit: width / 954
    readonly property int pageSize: 8

    // Message types shared with internal/ondevice/handler.go.
    readonly property int msgRequest: 1
    readonly property int msgResponse: 2
    readonly property int msgInstallEvent: 3
    readonly property int msgReady: 4

    property string screen: "starting"   // starting, list, detail, plan, progress
    property bool busy: false
    property string errorText: ""

    property var device: null
    property string tab: "all"   // all, installed, updates, system
    property var system: ({ canRestart: false, canRebuild: false })
    property var checks: []
    // The system job the confirm screen is asking about: restart or rebuild-hashtable.
    property string confirmAction: ""
    property int offset: 0
    property int total: 0
    property bool showIncompatible: false
    // The search applied to the list, and the one being typed on the search screen.
    property string query: ""
    property string draftQuery: ""
    property int draftMatches: -1
    property int hiddenIncompatible: 0
    property var packages: []
    property var upgrades: []
    property var detail: null
    property var plan: null
    // The screen a plan was opened from, to go back to on Cancel.
    property string planFrom: "detail"

    property string jobAction: "install"
    property var jobPackages: []
    property string jobMessage: ""
    property int jobIndex: 0
    property int jobTotal: 0
    property var jobResult: null

    property int nextId: 1
    // Requests waiting for a reply: id -> { callback, method, sentAt }.
    property var pending: ({})
    property bool connected: false

    // Requests that make Vellum refresh its index or solve may take a while; anything else that goes this long without a reply was lost.
    function replyLimit(method) {
        return method === "list" || method === "detail" || method === "job" || method === "device" ? 30000 : 180000
    }

    function request(method, params, onResult) {
        var id = nextId++
        pending[id] = { callback: onResult, method: method, sentAt: Date.now() }
        busy = true
        errorText = ""
        endpoint.sendMessage(msgRequest, JSON.stringify({ id: id, method: method, params: params || {} }))
    }

    // AppLoad drops whatever is sent while the backend is not connected, and its "frontend attached" notice can arrive before this screen is listening, so the screen keeps asking until the backend answers. Probes use id 0 and are not tracked as pending.
    function probe() {
        endpoint.sendMessage(msgRequest, JSON.stringify({ id: 0, method: "device", params: {} }))
    }

    function checkReplies() {
        var now = Date.now()
        var lost = []
        for (var id in pending) {
            if (now - pending[id].sentAt > replyLimit(pending[id].method))
                lost.push(id)
        }
        if (lost.length === 0)
            return
        for (var i = 0; i < lost.length; i++)
            delete pending[lost[i]]
        busy = Object.keys(pending).length > 0
        errorText = "The backend did not answer. Try again, or close reManager and open it again."
        if (screen === "starting")
            screen = "list"
    }

    function handleResponse(resp) {
        if (resp.id === 0) {
            if (!connected && !resp.error) {
                connected = true
                device = resp.result
                errorText = ""
                start()
            }
            return
        }
        var entry = pending[resp.id]
        delete pending[resp.id]
        busy = Object.keys(pending).length > 0
        if (resp.error) {
            errorText = resp.error
            if (screen === "starting")
                screen = "list"
            return
        }
        if (entry && entry.callback)
            entry.callback(resp.result)
    }

    function isSystemJob(action) {
        return action === "restart" || action === "rebuild-hashtable"
    }

    function start() {
        request("system", {}, function (result) { system = result })
        request("job", {}, function (job) {
            var events = job.events || []
            var last = events.length > 0 ? events[events.length - 1] : null
            var first = events.length > 0 ? events[0] : null
            // A restart or rebuild closes this app partway, so its result is shown when the app is next opened, if that is soon after.
            var recentSystemJob = last !== null && last.kind === "done" && first.kind === "started"
                && isSystemJob(first.action) && Date.now() / 1000 - last.at < 600
            if (job.running || recentSystemJob) {
                showJob(events)
            } else {
                loadList(0)
            }
        })
    }

    function openSearch() {
        draftQuery = query
        draftMatches = -1
        screen = "search"
        countMatches()
    }

    function editDraft(text) {
        draftQuery = text
        countTimer.restart()
    }

    // How many mods the search being typed would show, with the list's other filters. Asked for after a pause in typing, so each key does not cost a request.
    function countMatches() {
        var asked = draftQuery
        request("list", { installedOnly: tab === "installed", includeIncompatible: showIncompatible, query: asked, offset: 0, limit: 1 }, function (result) {
            if (asked === draftQuery)
                draftMatches = result.total
        })
    }

    function applySearch(text) {
        query = text.trim()
        loadList(0)
    }

    Timer {
        id: countTimer
        interval: 600
        onTriggered: root.countMatches()
    }

    function loadChecks() {
        checks = []
        request("checks", {}, function (result) { checks = result })
    }

    function fixCheck(fix) {
        if (fix === "rebuild-hashtable")
            confirmSystem("rebuild-hashtable")
        else if (fix === "resume-tripletap")
            request("resumeTripletap", {}, function () { loadChecks() })
        else if (fix === "install-tripletap")
            openPlan("plan", ["tripletap"])
    }

    function confirmSystem(action) {
        confirmAction = action
        screen = "confirm"
    }

    function startSystem() {
        jobAction = confirmAction
        jobPackages = []
        jobMessage = "Starting…"
        jobIndex = 0
        jobTotal = 0
        jobResult = null
        screen = "progress"
        request(confirmAction, { names: [] }, function () {})
    }

    function loadList(newOffset) {
        if (tab === "updates") {
            // Checking for updates refreshes Vellum's index, so it is asked for once per visit and paged here.
            if (newOffset > 0 && upgrades.length > 0) {
                showUpgrades(newOffset)
                return
            }
            packages = []
            total = 0
            offset = 0
            screen = "list"
            request("upgrades", {}, function (result) {
                upgrades = result
                showUpgrades(0)
            })
            return
        }
        request("list", { installedOnly: tab === "installed", includeIncompatible: showIncompatible, query: query, offset: newOffset, limit: pageSize }, function (result) {
            packages = result.packages
            total = result.total
            offset = result.offset
            hiddenIncompatible = result.incompatible
            screen = "list"
        })
    }

    function showUpgrades(newOffset) {
        total = upgrades.length
        offset = Math.min(newOffset, Math.max(0, total - 1))
        packages = upgrades.slice(offset, offset + pageSize)
        screen = "list"
    }

    function showTab(name) {
        tab = name
        upgrades = []
        if (name === "updates" || name === "system")
            query = ""
        if (name === "system") {
            packages = []
            total = 0
            screen = "list"
            request("system", {}, function (result) { system = result })
            loadChecks()
            return
        }
        loadList(0)
    }

    function openDetail(name) {
        request("detail", { name: name }, function (result) {
            detail = result
            screen = "detail"
        })
    }

    // method is plan, planRemove or planUpgrade; names is empty only for an upgrade of everything.
    function openPlan(method, names) {
        var from = screen
        request(method, { names: names }, function (result) {
            plan = result
            planFrom = from
            screen = "plan"
        })
    }

    function startJob() {
        jobAction = plan.action
        jobPackages = plan.requested
        jobMessage = "Starting…"
        jobIndex = 0
        jobTotal = 0
        jobResult = null
        screen = "progress"
        request(plan.action, { names: plan.requested }, function () {})
    }

    function describe(names) {
        return names.length > 0 ? names.join(", ") : "everything"
    }

    function showJob(events) {
        jobResult = null
        jobMessage = ""
        screen = "progress"
        for (var i = 0; i < events.length; i++)
            handleEvent(events[i])
    }

    function handleEvent(event) {
        if (event.kind === "started") {
            jobAction = event.action || "install"
            jobPackages = event.packages || []
            jobResult = null
            screen = "progress"
        } else if (event.kind === "progress") {
            if (event.progress.message)
                jobMessage = event.progress.message
            jobIndex = event.progress.index + 1
            jobTotal = event.progress.total
        } else if (event.kind === "done") {
            jobResult = event.result
        }
    }

    AppLoad {
        id: endpoint
        applicationID: "remanager"
        onMessageReceived: (type, contents) => {
            if (type === root.msgReady) {
                if (!root.connected)
                    root.probe()
                return
            }
            var data
            try {
                data = JSON.parse(contents)
            } catch (e) {
                root.errorText = "The backend sent something unreadable."
                return
            }
            if (type === root.msgResponse)
                root.handleResponse(data)
            else if (type === root.msgInstallEvent)
                root.handleEvent(data)
        }
    }

    Timer {
        interval: 700
        repeat: true
        triggeredOnStart: true
        running: !root.connected
        onTriggered: root.probe()
    }

    // Only a message, not a dead end: probing carries on, and the screen fills in if the backend answers later.
    Timer {
        interval: 10000
        running: !root.connected
        onTriggered: {
            root.errorText = "The backend has not answered yet. If this stays, close reManager and open it again."
            root.screen = "list"
        }
    }

    Timer {
        interval: 2000
        repeat: true
        running: root.busy
        onTriggered: root.checkReplies()
    }

    // ---- header ----
    Item {
        id: header
        anchors { top: parent.top; left: parent.left; right: parent.right }
        height: 150 * unit

        Text {
            anchors { left: parent.left; leftMargin: 40 * unit; top: parent.top; topMargin: 28 * unit }
            text: "reManager"
            font.pixelSize: 52 * unit
            font.bold: true
        }
        Text {
            anchors { left: parent.left; leftMargin: 40 * unit; right: closeButton.left; rightMargin: 24 * unit; bottom: parent.bottom; bottomMargin: 18 * unit }
            text: root.device ? root.device.name.replace("reMarkable ", "") + " · OS " + root.device.firmware + (root.device.tested ? "" : " · untested model")
                + (root.device.appVersion ? " · v" + root.device.appVersion : "") : ""
            elide: Text.ElideRight
            font.pixelSize: 28 * unit
            color: "#444444"
        }
        TapButton {
            id: closeButton
            anchors { right: parent.right; rightMargin: 40 * unit; verticalCenter: parent.verticalCenter }
            unit: root.unit
            label: "Close"
            onTapped: root.close()
        }
        Rectangle {
            anchors { left: parent.left; right: parent.right; bottom: parent.bottom }
            height: Math.max(2, 3 * unit)
            color: "black"
        }
    }

    // ---- error line ----
    Rectangle {
        id: errorBar
        anchors { top: header.bottom; left: parent.left; right: parent.right }
        height: visible ? errorLabel.implicitHeight + 40 * unit : 0
        visible: root.errorText !== ""
        color: "white"
        border.color: "black"
        border.width: Math.max(2, 3 * unit)

        Text {
            id: errorLabel
            anchors { left: parent.left; right: parent.right; margins: 40 * unit; verticalCenter: parent.verticalCenter }
            text: "Problem: " + root.errorText
            wrapMode: Text.Wrap
            maximumLineCount: 6
            elide: Text.ElideRight
            font.pixelSize: 30 * unit
        }
    }

    Item {
        id: body
        anchors { top: errorBar.bottom; left: parent.left; right: parent.right; bottom: parent.bottom }

        // ---- starting ----
        Text {
            visible: root.screen === "starting"
            anchors.centerIn: parent
            text: "Starting…"
            font.pixelSize: 40 * unit
        }

        // ---- list ----
        Item {
            visible: root.screen === "list"
            anchors.fill: parent

            Row {
                id: tabs
                anchors { top: parent.top; topMargin: 24 * unit; left: parent.left; leftMargin: 40 * unit }
                spacing: 20 * unit
                TapButton {
                    unit: root.unit
                    label: "All mods"
                    inverted: root.tab === "all"
                    onTapped: root.showTab("all")
                }
                TapButton {
                    unit: root.unit
                    label: "Installed"
                    inverted: root.tab === "installed"
                    onTapped: root.showTab("installed")
                }
                TapButton {
                    unit: root.unit
                    label: "Updates"
                    inverted: root.tab === "updates"
                    onTapped: root.showTab("updates")
                }
                TapButton {
                    unit: root.unit
                    label: "System"
                    inverted: root.tab === "system"
                    onTapped: root.showTab("system")
                }
            }

            Column {
                visible: root.tab === "system"
                anchors { top: tabs.bottom; topMargin: 36 * unit; left: parent.left; right: parent.right; leftMargin: 40 * unit; rightMargin: 40 * unit }
                spacing: 20 * unit

                Text {
                    text: "Status"
                    font.pixelSize: 38 * unit
                    font.bold: true
                }
                Text {
                    visible: root.checks.length === 0
                    text: root.busy ? "Checking…" : "Nothing to report."
                    font.pixelSize: 30 * unit
                    color: "#444444"
                }
                Repeater {
                    model: root.checks
                    delegate: Item {
                        width: parent.width
                        height: Math.max(checkText.implicitHeight, fixButton.visible ? fixButton.height : 0) + 12 * root.unit

                        Text {
                            id: checkMark
                            anchors { left: parent.left; top: parent.top }
                            text: modelData.ok ? "✓" : "!"
                            font.pixelSize: 34 * root.unit
                            font.bold: true
                            width: 44 * root.unit
                        }
                        Column {
                            id: checkText
                            anchors { left: checkMark.right; right: fixButton.visible ? fixButton.left : parent.right; rightMargin: 20 * root.unit; top: parent.top }
                            spacing: 4 * root.unit
                            Text {
                                width: parent.width
                                text: modelData.title
                                wrapMode: Text.Wrap
                                font.pixelSize: 32 * root.unit
                                font.bold: !modelData.ok
                            }
                            Text {
                                width: parent.width
                                visible: !!modelData.detail
                                text: modelData.detail || ""
                                wrapMode: Text.Wrap
                                font.pixelSize: 26 * root.unit
                                color: "#444444"
                            }
                        }
                        SmallButton {
                            id: fixButton
                            anchors { right: parent.right; top: parent.top }
                            visible: !!modelData.fix
                            unit: root.unit
                            enabled: !root.busy
                            label: modelData.fix === "rebuild-hashtable" ? "Rebuild…" : modelData.fix === "resume-tripletap" ? "Turn on" : "Install…"
                            onTapped: root.fixCheck(modelData.fix)
                        }
                    }
                }

                Item { width: 1; height: 16 * root.unit }
                Text {
                    text: "Actions"
                    font.pixelSize: 38 * unit
                    font.bold: true
                }
                Text {
                    width: parent.width
                    text: "Both restart the reMarkable UI, and this app closes while they run. Restart applies mod changes; rebuild the hashtable after an OS update, or when a mod asks for it."
                    wrapMode: Text.Wrap
                    font.pixelSize: 28 * unit
                    color: "#444444"
                }
                Row {
                    spacing: 20 * root.unit
                    TapButton {
                        unit: root.unit
                        label: "Restart…"
                        enabled: root.system.canRestart && !root.busy
                        onTapped: root.confirmSystem("restart")
                    }
                    TapButton {
                        unit: root.unit
                        label: "Rebuild hashtable…"
                        enabled: root.system.canRebuild && !root.busy
                        onTapped: root.confirmSystem("rebuild-hashtable")
                    }
                }
            }

            Item {
                id: osFilter
                anchors { top: tabs.bottom; left: parent.left; right: parent.right }
                readonly property bool shown: root.tab === "all" || root.tab === "installed"
                readonly property bool osShown: root.tab === "all" && (root.showIncompatible || root.hiddenIncompatible > 0)
                visible: shown
                height: shown ? 92 * unit : 0

                Row {
                    anchors { left: parent.left; leftMargin: 40 * unit; verticalCenter: parent.verticalCenter }
                    spacing: 16 * unit
                    SmallButton {
                        unit: root.unit
                        label: root.query === "" ? "Search" : "Search: " + (root.query.length > 14 ? root.query.substring(0, 13) + "…" : root.query)
                        enabled: !root.busy
                        onTapped: root.openSearch()
                    }
                    SmallButton {
                        visible: root.query !== ""
                        unit: root.unit
                        label: "Clear"
                        enabled: !root.busy
                        onTapped: root.applySearch("")
                    }
                }
                Row {
                    visible: osFilter.osShown
                    anchors { right: parent.right; rightMargin: 40 * unit; verticalCenter: parent.verticalCenter }
                    spacing: 16 * unit
                    Text {
                        anchors.verticalCenter: parent.verticalCenter
                        text: root.showIncompatible ? "All OS versions" : root.hiddenIncompatible + " not for this OS"
                        font.pixelSize: 26 * unit
                        color: "#444444"
                    }
                    SmallButton {
                        unit: root.unit
                        label: root.showIncompatible ? "Hide" : "Show"
                        enabled: !root.busy
                        onTapped: { root.showIncompatible = !root.showIncompatible; root.loadList(0) }
                    }
                }
            }

            Column {
                id: rows
                anchors { top: osFilter.bottom; topMargin: 24 * unit; left: parent.left; right: parent.right }

                Repeater {
                    model: root.packages
                    delegate: Item {
                        width: rows.width
                        height: 132 * root.unit

                        Rectangle {
                            anchors { left: parent.left; right: parent.right; top: parent.top }
                            height: Math.max(1, 2 * root.unit)
                            color: "#888888"
                        }
                        Text {
                            id: rowName
                            anchors { left: parent.left; leftMargin: 40 * root.unit; top: parent.top; topMargin: 20 * root.unit }
                            width: parent.width - rowState.width - 120 * root.unit
                            text: modelData.name
                            elide: Text.ElideRight
                            font.pixelSize: 36 * root.unit
                            font.bold: true
                            color: root.tab === "updates" || modelData.compatible || modelData.installed ? "black" : "#777777"
                        }
                        Text {
                            id: rowState
                            anchors { right: parent.right; rightMargin: 40 * root.unit; baseline: rowName.baseline }
                            text: root.tab === "updates" ? "" : modelData.installed ? "Installed" : modelData.compatible ? "" : "Not for this OS"
                            font.pixelSize: 28 * root.unit
                        }
                        Text {
                            anchors { left: parent.left; leftMargin: 40 * root.unit; right: parent.right; rightMargin: 40 * root.unit; top: rowName.bottom; topMargin: 6 * root.unit }
                            text: root.tab === "updates" ? (modelData.from ? modelData.from + "  →  " + modelData.to : "New: " + modelData.to) : (modelData.description || "")
                            elide: Text.ElideRight
                            maximumLineCount: 1
                            font.pixelSize: 28 * root.unit
                            color: "#444444"
                        }
                        MouseArea {
                            anchors.fill: parent
                            onClicked: root.tab === "updates" ? root.openPlan("planUpgrade", [modelData.name]) : root.openDetail(modelData.name)
                        }
                    }
                }
            }
            Text {
                visible: root.tab !== "system" && !root.busy && root.packages.length === 0 && root.errorText === ""
                anchors.centerIn: parent
                text: root.tab === "updates" ? "Everything is up to date."
                    : root.query !== "" ? "No mods match “" + root.query + "”."
                    : root.tab === "installed" ? "Nothing installed yet." : "The catalogue is empty."
                font.pixelSize: 34 * unit
            }

            Item {
                visible: root.tab !== "system"
                anchors { left: parent.left; right: parent.right; bottom: parent.bottom; bottomMargin: 36 * unit }
                height: 96 * unit
                TapButton {
                    anchors { left: parent.left; leftMargin: 40 * unit }
                    unit: root.unit
                    label: "Previous"
                    enabled: root.offset > 0 && !root.busy
                    onTapped: root.loadList(Math.max(0, root.offset - root.pageSize))
                }
                Text {
                    visible: !updateAll.visible
                    anchors.centerIn: parent
                    text: root.busy ? (root.tab === "updates" ? "Checking…" : "Loading…")
                        : "Page " + (Math.floor(root.offset / root.pageSize) + 1) + " of " + Math.max(1, Math.ceil(root.total / root.pageSize))
                          + "\n" + root.total + (root.tab === "updates" ? (root.total === 1 ? " update" : " updates") : (root.total === 1 ? " mod" : " mods"))
                    horizontalAlignment: Text.AlignHCenter
                    font.pixelSize: 30 * unit
                }
                TapButton {
                    id: updateAll
                    anchors.centerIn: parent
                    unit: root.unit
                    visible: root.tab === "updates" && root.total > 0 && !root.busy
                    label: "Update all…"
                    inverted: true
                    onTapped: root.openPlan("planUpgrade", [])
                }
                TapButton {
                    anchors { right: parent.right; rightMargin: 40 * unit }
                    unit: root.unit
                    label: "Next"
                    enabled: root.offset + root.pageSize < root.total && !root.busy
                    onTapped: root.loadList(root.offset + root.pageSize)
                }
            }
        }

        // ---- detail ----
        Item {
            visible: root.screen === "detail" && root.detail !== null
            anchors.fill: parent

            Column {
                anchors { top: parent.top; topMargin: 36 * unit; left: parent.left; right: parent.right; margins: 40 * unit }
                spacing: 20 * unit

                Text {
                    width: parent.width
                    text: root.detail ? root.detail.name : ""
                    wrapMode: Text.Wrap
                    font.pixelSize: 48 * unit
                    font.bold: true
                }
                Text {
                    width: parent.width
                    text: !root.detail ? "" : root.detail.installed
                        ? "Installed: " + root.detail.installedVersion + (root.detail.installedVersion !== root.detail.version ? "   ·   Catalogue: " + root.detail.version : "")
                        : "Version " + root.detail.version
                    font.pixelSize: 30 * unit
                }
                Text {
                    width: parent.width
                    text: root.detail ? root.detail.description : ""
                    wrapMode: Text.Wrap
                    font.pixelSize: 34 * unit
                }
                Text {
                    width: parent.width
                    visible: text !== ""
                    text: !root.detail ? "" : [
                        root.detail.author ? "By " + root.detail.author : "",
                        root.detail.license ? "Licence: " + root.detail.license : "",
                        root.detail.category ? "Category: " + root.detail.category : ""
                    ].filter(function (s) { return s !== "" }).join("   ·   ")
                    wrapMode: Text.Wrap
                    font.pixelSize: 28 * unit
                    color: "#444444"
                }
                Text {
                    width: parent.width
                    visible: root.detail !== null && (root.detail.depends || []).length > 0
                    text: root.detail && root.detail.depends ? "Needs: " + root.detail.depends.join(", ") : ""
                    wrapMode: Text.Wrap
                    font.pixelSize: 28 * unit
                    color: "#444444"
                }
                Text {
                    width: parent.width
                    visible: root.detail !== null && !root.detail.compatible && !root.detail.installed
                    text: root.detail && root.detail.incompatibleReason ? "Cannot be installed here: " + root.detail.incompatibleReason : "Cannot be installed on this tablet or OS version."
                    wrapMode: Text.Wrap
                    font.pixelSize: 30 * unit
                    font.bold: true
                }
                Text {
                    width: parent.width
                    visible: root.detail !== null && root.detail.installed && root.detail.protected
                    text: "This app runs on it, so it can only be removed from a computer."
                    wrapMode: Text.Wrap
                    font.pixelSize: 28 * unit
                    color: "#444444"
                }
            }

            Item {
                anchors { left: parent.left; right: parent.right; bottom: parent.bottom; bottomMargin: 36 * unit }
                height: 96 * unit
                TapButton {
                    anchors { left: parent.left; leftMargin: 40 * unit }
                    unit: root.unit
                    label: "Back"
                    onTapped: root.screen = "list"
                }
                TapButton {
                    anchors { right: parent.right; rightMargin: 40 * unit }
                    unit: root.unit
                    visible: root.detail !== null && !root.detail.installed
                    label: root.busy ? "Checking…" : "Install…"
                    inverted: true
                    enabled: root.detail !== null && root.detail.compatible && !root.busy
                    onTapped: root.openPlan("plan", [root.detail.name])
                }
                TapButton {
                    anchors { right: parent.right; rightMargin: 40 * unit }
                    unit: root.unit
                    visible: root.detail !== null && root.detail.installed && !root.detail.protected
                    label: root.busy ? "Checking…" : "Remove…"
                    enabled: !root.busy
                    onTapped: root.openPlan("planRemove", [root.detail.name])
                }
            }
        }

        // ---- plan ----
        Item {
            visible: root.screen === "plan" && root.plan !== null
            anchors.fill: parent

            readonly property bool blocked: root.plan !== null && !!root.plan.error
            readonly property bool nothing: root.plan !== null && !root.plan.error && root.plan.packages.length === 0
            readonly property string action: root.plan ? root.plan.action : "install"
            readonly property string verb: action === "remove" ? "Remove" : action === "upgrade" ? "Update" : "Install"

            Column {
                anchors { top: parent.top; topMargin: 36 * unit; left: parent.left; right: parent.right; margins: 40 * unit }
                spacing: 24 * unit

                Text {
                    width: parent.width
                    text: !root.plan ? "" : parent.parent.blocked ? "Cannot " + parent.parent.verb.toLowerCase() + " " + root.describe(root.plan.requested)
                        : parent.parent.nothing ? "Nothing to do"
                        : parent.parent.verb + " " + root.describe(root.plan.requested) + "?"
                    wrapMode: Text.Wrap
                    font.pixelSize: 44 * unit
                    font.bold: true
                }
                Text {
                    width: parent.width
                    visible: parent.parent.blocked
                    text: root.plan && root.plan.error ? root.plan.error : ""
                    wrapMode: Text.Wrap
                    maximumLineCount: 24
                    elide: Text.ElideRight
                    font.pixelSize: 28 * unit
                    font.family: "monospace"
                }
                Text {
                    width: parent.width
                    visible: parent.parent.nothing
                    text: parent.parent.action === "upgrade" ? "Everything is up to date." : "Everything this needs is already installed."
                    wrapMode: Text.Wrap
                    font.pixelSize: 34 * unit
                }
                Text {
                    width: parent.width
                    visible: !parent.parent.blocked && !parent.parent.nothing
                    text: !root.plan ? "" : (parent.parent.action === "remove" ? "This will remove " : parent.parent.action === "upgrade" ? "This will change " : "This will install or change ")
                        + root.plan.packages.length + (root.plan.packages.length === 1 ? " package:" : " packages:")
                    font.pixelSize: 32 * unit
                }
                Text {
                    width: parent.width
                    visible: !parent.parent.blocked && !parent.parent.nothing
                    text: !root.plan ? "" : root.plan.packages.map(function (name) {
                        var change = root.plan.upgrades ? root.plan.upgrades[name] : undefined
                        return "•  " + name + (change ? "   (" + change + ")" : "")
                    }).join("\n")
                    wrapMode: Text.Wrap
                    maximumLineCount: 20
                    elide: Text.ElideRight
                    lineHeight: 1.25
                    font.pixelSize: 32 * unit
                }
                Text {
                    width: parent.width
                    visible: !parent.parent.blocked && !parent.parent.nothing && root.plan !== null && !!root.plan.keep && root.plan.keep.length > 0
                    text: root.plan && root.plan.keep ? root.plan.keep.join(", ") + " will be recorded as installed by you, so the mods that need it keep it. Nothing else about it changes." : ""
                    wrapMode: Text.Wrap
                    font.pixelSize: 30 * unit
                }
                Text {
                    width: parent.width
                    visible: !parent.parent.blocked && !parent.parent.nothing
                    text: parent.parent.action === "remove"
                        ? "A removed mod may stay visible until xovi is restarted."
                        : parent.parent.action === "upgrade"
                        ? "The tablet needs Wi-Fi for the download. Updated mods take effect after xovi is restarted."
                        : "The tablet needs Wi-Fi for the download. Steps that restart the reMarkable screen, such as rebuilding the hashtable, are not run by this app yet; it will tell you if one is left to do."
                    wrapMode: Text.Wrap
                    font.pixelSize: 28 * unit
                    color: "#444444"
                }
            }

            Item {
                anchors { left: parent.left; right: parent.right; bottom: parent.bottom; bottomMargin: 36 * unit }
                height: 96 * unit
                TapButton {
                    anchors { left: parent.left; leftMargin: 40 * unit }
                    unit: root.unit
                    label: parent.parent.blocked || parent.parent.nothing ? "Back" : "Cancel"
                    onTapped: root.screen = root.planFrom
                }
                TapButton {
                    anchors { right: parent.right; rightMargin: 40 * unit }
                    unit: root.unit
                    visible: !parent.parent.blocked && !parent.parent.nothing
                    label: parent.parent.verb
                    inverted: true
                    onTapped: root.startJob()
                }
            }
        }

        // ---- search ----
        Item {
            visible: root.screen === "search"
            anchors.fill: parent

            Text {
                id: searchTitle
                anchors { top: parent.top; topMargin: 36 * unit; left: parent.left; leftMargin: 40 * unit }
                text: root.tab === "installed" ? "Search installed mods" : "Search mods"
                font.pixelSize: 44 * unit
                font.bold: true
            }
            Rectangle {
                id: searchBox
                anchors { top: searchTitle.bottom; topMargin: 28 * unit; left: parent.left; right: parent.right; leftMargin: 40 * unit; rightMargin: 40 * unit }
                height: 100 * unit
                border.color: "black"
                border.width: Math.max(2, 3 * unit)
                Text {
                    anchors { left: parent.left; leftMargin: 24 * unit; right: clearDraft.left; rightMargin: 16 * unit; verticalCenter: parent.verticalCenter }
                    text: root.draftQuery === "" ? "Name or description" : root.draftQuery + "▏"
                    color: root.draftQuery === "" ? "#888888" : "black"
                    elide: Text.ElideLeft
                    font.pixelSize: 40 * unit
                }
                SmallButton {
                    id: clearDraft
                    anchors { right: parent.right; rightMargin: 18 * unit; verticalCenter: parent.verticalCenter }
                    visible: root.draftQuery !== ""
                    unit: root.unit
                    label: "Clear"
                    onTapped: root.editDraft("")
                }
            }
            Text {
                anchors { top: searchBox.bottom; topMargin: 20 * unit; left: parent.left; leftMargin: 40 * unit }
                text: root.draftMatches < 0 ? " " : root.draftMatches === 0 ? "No mods match" : root.draftMatches + (root.draftMatches === 1 ? " mod matches" : " mods match")
                font.pixelSize: 30 * unit
                color: "#444444"
            }

            SearchKeyboard {
                anchors { left: parent.left; right: parent.right; bottom: searchButtons.top; leftMargin: 40 * unit; rightMargin: 40 * unit; bottomMargin: 48 * unit }
                height: implicitHeight
                unit: root.unit
                onTyped: (character) => {
                    if (root.draftQuery.length < 40 && !(character === " " && (root.draftQuery === "" || root.draftQuery.endsWith(" "))))
                        root.editDraft(root.draftQuery + character)
                }
                onBackspace: root.editDraft(root.draftQuery.slice(0, -1))
            }

            Item {
                id: searchButtons
                anchors { left: parent.left; right: parent.right; bottom: parent.bottom; bottomMargin: 36 * unit }
                height: 96 * unit
                TapButton {
                    anchors { left: parent.left; leftMargin: 40 * unit }
                    unit: root.unit
                    label: "Cancel"
                    onTapped: root.screen = "list"
                }
                TapButton {
                    anchors { right: parent.right; rightMargin: 40 * unit }
                    unit: root.unit
                    label: root.draftQuery.trim() === "" ? "Show all" : "Show results"
                    inverted: true
                    onTapped: root.applySearch(root.draftQuery)
                }
            }
        }

        // ---- confirm a restart or rebuild ----
        Item {
            visible: root.screen === "confirm"
            anchors.fill: parent

            Column {
                anchors { top: parent.top; topMargin: 36 * unit; left: parent.left; right: parent.right; margins: 40 * unit }
                spacing: 24 * unit

                Text {
                    width: parent.width
                    text: root.confirmAction === "restart" ? "Restart the reMarkable UI?" : "Rebuild the hashtable?"
                    wrapMode: Text.Wrap
                    font.pixelSize: 44 * unit
                    font.bold: true
                }
                Text {
                    width: parent.width
                    text: root.confirmAction === "restart"
                        ? "The screen goes blank for a few seconds and the reMarkable UI starts again with mods. This app closes; open it again afterwards to see the result."
                        : "Takes one to two minutes, and the screen restarts twice.\n\n"
                          + "1.  The screen restarts into a temporary reMarkable UI. It looks normal but has no AppLoad and no mods. If you have a passcode, enter it.\n"
                          + "2.  Leave the tablet alone until the screen restarts a second time. That is the sign it has finished.\n\n"
                          + "Triple-tap is turned off until then. This app closes; open it again afterwards to see the result."
                    wrapMode: Text.Wrap
                    font.pixelSize: 32 * unit
                }
                Text {
                    width: parent.width
                    text: "If the reMarkable UI does not come back, hold the power button to restart the tablet. It always starts without mods."
                    wrapMode: Text.Wrap
                    font.pixelSize: 28 * unit
                    color: "#444444"
                }
            }

            Item {
                anchors { left: parent.left; right: parent.right; bottom: parent.bottom; bottomMargin: 36 * unit }
                height: 96 * unit
                TapButton {
                    anchors { left: parent.left; leftMargin: 40 * unit }
                    unit: root.unit
                    label: "Cancel"
                    onTapped: root.screen = root.tab === "system" ? "list" : "progress"
                }
                TapButton {
                    anchors { right: parent.right; rightMargin: 40 * unit }
                    unit: root.unit
                    label: root.confirmAction === "restart" ? "Restart" : "Rebuild"
                    inverted: true
                    onTapped: root.startSystem()
                }
            }
        }

        // ---- progress ----
        Item {
            visible: root.screen === "progress"
            anchors.fill: parent

            readonly property bool done: root.jobResult !== null
            readonly property bool failed: done && !root.jobResult.success
            readonly property var words: root.jobAction === "remove" ? ["Removing", "Removed", "Removal failed"]
                : root.jobAction === "upgrade" ? ["Updating", "Updated", "Update failed"]
                : root.jobAction === "restart" ? ["Restarting the reMarkable UI", "Restarted the reMarkable UI", "Restart failed"]
                : root.jobAction === "rebuild-hashtable" ? ["Rebuilding the hashtable", "Rebuilt the hashtable", "Rebuild failed"]
                : ["Installing", "Installed", "Install failed"]
            readonly property bool system: root.isSystemJob(root.jobAction)
            readonly property bool hashtableLeft: done && !!root.jobResult.deferred && root.jobResult.deferred.some(function (s) { return s.toLowerCase().indexOf("hashtable") >= 0 })

            Column {
                anchors { top: parent.top; topMargin: 36 * unit; left: parent.left; right: parent.right; margins: 40 * unit }
                spacing: 24 * unit

                Text {
                    width: parent.width
                    text: parent.parent.system ? parent.parent.words[!parent.parent.done ? 0 : parent.parent.failed ? 2 : 1]
                        : !parent.parent.done ? parent.parent.words[0] + " " + root.describe(root.jobPackages)
                        : parent.parent.failed ? parent.parent.words[2] : parent.parent.words[1] + " " + root.describe(root.jobPackages)
                    wrapMode: Text.Wrap
                    font.pixelSize: 44 * unit
                    font.bold: true
                }
                Text {
                    width: parent.width
                    visible: !parent.parent.done
                    text: (root.jobTotal > 0 ? "Step " + root.jobIndex + " of " + root.jobTotal + "\n" : "") + root.jobMessage
                    wrapMode: Text.Wrap
                    maximumLineCount: 6
                    elide: Text.ElideRight
                    font.pixelSize: 32 * unit
                }
                Text {
                    width: parent.width
                    visible: !parent.parent.done
                    text: parent.parent.system
                        ? (root.jobAction === "rebuild-hashtable"
                            ? "The screen is about to restart into a temporary UI without mods. Enter your passcode if asked, then wait for the second restart. Open this app again afterwards to see the result."
                            : "The reMarkable UI is about to restart and this app will close. Open it again afterwards to see the result.")
                        : "You can close this screen. The work carries on, and this page shows where it got to when you come back."
                    wrapMode: Text.Wrap
                    font.pixelSize: 28 * unit
                    color: "#444444"
                }
                Text {
                    width: parent.width
                    visible: parent.parent.failed
                    text: root.jobResult && root.jobResult.errors ? root.jobResult.errors.join("\n\n") : ""
                    wrapMode: Text.Wrap
                    maximumLineCount: 20
                    elide: Text.ElideRight
                    font.pixelSize: 30 * unit
                }
                Text {
                    width: parent.width
                    visible: parent.parent.done && root.jobResult.deferred !== undefined && root.jobResult.deferred.length > 0
                    text: root.jobResult && root.jobResult.deferred ? "Still to do:\n" + root.jobResult.deferred.map(function (s) { return "•  " + s }).join("\n") : ""
                    wrapMode: Text.Wrap
                    lineHeight: 1.25
                    font.pixelSize: 32 * unit
                }
                Text {
                    width: parent.width
                    visible: parent.parent.done && !parent.parent.failed && !parent.parent.system
                    text: parent.parent.hashtableLeft
                        ? "UI mods need the hashtable rebuilt before they work. That also restarts the reMarkable UI, so it is the only restart needed."
                        : "Some changes only show after the reMarkable UI is restarted with mods."
                    wrapMode: Text.Wrap
                    font.pixelSize: 28 * unit
                    color: "#444444"
                }
            }

            TapButton {
                anchors { left: parent.left; leftMargin: 40 * unit; bottom: parent.bottom; bottomMargin: 36 * unit }
                unit: root.unit
                visible: parent.done && !parent.failed && !parent.system && (parent.hashtableLeft ? root.system.canRebuild : root.system.canRestart)
                label: parent.hashtableLeft ? "Rebuild hashtable…" : "Restart now…"
                onTapped: root.confirmSystem(parent.hashtableLeft ? "rebuild-hashtable" : "restart")
            }
            TapButton {
                anchors { right: parent.right; rightMargin: 40 * unit; bottom: parent.bottom; bottomMargin: 36 * unit }
                unit: root.unit
                visible: parent.done
                label: "Done"
                inverted: true
                onTapped: { root.upgrades = []; if (root.tab === "system") root.showTab("all"); else root.loadList(root.tab === "updates" ? 0 : root.offset) }
            }
        }
    }
}
