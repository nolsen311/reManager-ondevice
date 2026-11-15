import QtQuick 2.15

// A compact bordered button for secondary actions in a row of text.
Rectangle {
    id: button

    property string label: ""
    property real unit: 1
    property bool enabled: true
    signal tapped

    width: text.implicitWidth + 40 * unit
    height: 64 * unit
    color: "white"
    border.color: enabled ? "black" : "#999999"
    border.width: Math.max(2, 2 * unit)

    Text {
        id: text
        anchors.centerIn: parent
        text: button.label
        font.pixelSize: 28 * button.unit
        color: button.enabled ? "black" : "#999999"
    }

    MouseArea {
        anchors.fill: parent
        enabled: button.enabled
        onClicked: button.tapped()
    }
}
