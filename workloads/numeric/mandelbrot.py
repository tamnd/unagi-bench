# unagi-bench workload
# tier: 1
# tag: numeric
# desc: mandelbrot escape counts over a small grid, float-heavy inner loop

def count(width, height, limit):
    total = 0
    y = 0
    while y < height:
        x = 0
        while x < width:
            cr = -2.0 + 3.0 * float(x) / float(width)
            ci = -1.5 + 3.0 * float(y) / float(height)
            zr = 0.0
            zi = 0.0
            it = 0
            while it < limit:
                nzr = zr * zr - zi * zi + cr
                nzi = 2.0 * zr * zi + ci
                zr = nzr
                zi = nzi
                if zr * zr + zi * zi > 4.0:
                    break
                it += 1
            total += it
            x += 1
        y += 1
    return total


print(count(200, 200, 100))
