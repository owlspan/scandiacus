pub struct Box<T> {
    item: T
}

impl Box<T> {
    fn get(self) -> T {
        return self.item
    }
}

pub fn wrap<T>(v: T) -> Box<T> {
    return Box<T>{item: v}
}

pub fn biggest<T>(xs: []T, better: fn(T, T) -> bool) -> T {
    let best = xs[0]
    for x in xs {
        if better(x, best) {
            best = x
        }
    }
    return best
}
