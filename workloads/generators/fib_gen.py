# unagi-bench workload
# tier: 2
# tag: generators
# desc: a modular fibonacci generator drained in a loop, the state-machine lowering target

def fibs(limit):
    a = 0
    b = 1
    i = 0
    while i < limit:
        yield a
        c = (a + b) % 1000000007
        a = b
        b = c
        i += 1


def run(limit):
    total = 0
    for v in fibs(limit):
        total += v % 1000
    return total


print(run(2000000))
