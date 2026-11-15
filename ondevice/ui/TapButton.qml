import QtQuick 2.15

// A bordered button sized for a finger. No hover or animation: e-ink shows neither well.
Rectangle {
    id: button

    property string label: ""
    property bool inverted: false
    property bool enabled: true
    property real unit: 1
    signal tapped

    implicitWidth: text.implicitWidth + 56 * unit
    implicitHeight: 96 * unit
    width: implicitWidth
    height: implicitHeight
    color: inverted && enabled ? "black" : "white"
    border.color: enabled ? "black" : "#999999"
    border.width: Math.max(2, 3 * unit)

    Text {
        id: text
        anchors.centerIn: parent
        text: button.label
        color: !button.enabled ? "#999999" : button.inverted ? "white" : "black"
        font.pixelSize: 36 * button.unit
        font.bold: button.inverted
    }

    MouseArea {
        anchors.fill: parent
        enabled: button.enabled
        onClicked: button.tapped()
    }
}
