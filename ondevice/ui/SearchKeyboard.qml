import QtQuick 2.15

// An on-screen keyboard for search. Mod names and descriptions only need letters, digits, hyphens and spaces.
// AppLoad's own keyboard only feeds external apps, so a QML app brings its own.
Item {
    id: keyboard

    property real unit: 1
    signal typed(string character)
    signal backspace

    readonly property var rows: ["1234567890", "qwertyuiop", "asdfghjkl-", "zxcvbnm"]
    readonly property real gap: 8 * unit
    readonly property real keyWidth: (width - gap * 9) / 10
    readonly property real keyHeight: 100 * unit

    implicitHeight: keyHeight * 5 + gap * 4

    Column {
        anchors.fill: parent
        spacing: keyboard.gap

        Repeater {
            model: keyboard.rows
            delegate: Row {
                spacing: keyboard.gap
                anchors.horizontalCenter: parent.horizontalCenter

                Repeater {
                    model: modelData.split("")
                    delegate: Rectangle {
                        width: keyboard.keyWidth
                        height: keyboard.keyHeight
                        border.color: "black"
                        border.width: Math.max(2, 2 * keyboard.unit)
                        Text {
                            anchors.centerIn: parent
                            text: modelData
                            font.pixelSize: 40 * keyboard.unit
                        }
                        MouseArea {
                            anchors.fill: parent
                            onClicked: keyboard.typed(modelData)
                        }
                    }
                }
                // The last row ends with backspace, two keys wide.
                Rectangle {
                    visible: index === keyboard.rows.length - 1
                    width: keyboard.keyWidth * 2 + keyboard.gap
                    height: keyboard.keyHeight
                    border.color: "black"
                    border.width: Math.max(2, 2 * keyboard.unit)
                    Text {
                        anchors.centerIn: parent
                        text: "Delete"
                        font.pixelSize: 32 * keyboard.unit
                    }
                    MouseArea {
                        anchors.fill: parent
                        onClicked: keyboard.backspace()
                    }
                }
            }
        }

        Rectangle {
            anchors.horizontalCenter: parent.horizontalCenter
            width: keyboard.keyWidth * 6 + keyboard.gap * 5
            height: keyboard.keyHeight
            border.color: "black"
            border.width: Math.max(2, 2 * keyboard.unit)
            Text {
                anchors.centerIn: parent
                text: "space"
                font.pixelSize: 32 * keyboard.unit
            }
            MouseArea {
                anchors.fill: parent
                onClicked: keyboard.typed(" ")
            }
        }
    }
}
