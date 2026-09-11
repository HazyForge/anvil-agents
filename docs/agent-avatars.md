# Anvil Agents faces

Operator avatars for **Anvil Agents** (not Anvil Primaris) live in
[`assets/agent-avatars/`](../assets/agent-avatars/README.md).

They are a family of SVG eyes with a documented idle/blink hook so Anvil
Agents Console, later Anvil Agents Desktop, and later iOS/Android can share
one scene graph. Raster portraits are gone. Do not add CSS-only animation as
the contract.

Canonical stored value: face id on `ui.anvil.hazyforge.io/icon` (`forge`,
`herald`, …). Legacy `robot-NN.jpg` paths still resolve.
