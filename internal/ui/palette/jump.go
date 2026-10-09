package palette

// jumpLabel marks a hit this session has not read, so it never reads like a
// cached issue whose age happened to come back blank.
const jumpLabel = "not cached — opens with a fetch"

func jumpHit(key string) hit { return hit{key: key, text: key + "  " + jumpLabel} }
