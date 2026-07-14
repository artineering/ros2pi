package hostfacts

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
)

// probeGPIOChips enumerates GPIO chips and identifies which one drives the
// 40-pin header.
//
// Chip NUMBER is never trusted. Kernel 6.6.47 flipped the Pi 5 header from
// gpiochip4 back to gpiochip0, breaking shipping projects; on this Pi 4 the
// header is gpiochip0 but /dev/gpiochip1 also exists (raspberrypi-exp-gpio).
// Only the label identifies the chip.
//
// The label is read via the chardev ioctl, which is the supported ABI. Where
// that fails (permission, or a kernel without it) we fall back to sysfs at
// /sys/class/gpio/chipN/label -- index-aligned with gpiochipN, and verified on
// a Pi 4 to agree exactly with the ioctl. Note that sysfs GPIO is deprecated,
// which is why it is the fallback and not the primary.
func probeGPIOChips(io gpioIO) ([]GPIOChip, []Warning) {
	var chips []GPIOChip
	var warns []Warning

	names, err := devGPIOChipNames(io)
	if err != nil {
		return nil, []Warning{{Code: "gpio.enumerate", Detail: err.Error()}}
	}

	for _, name := range names {
		dev := "/dev/" + name
		c := GPIOChip{Name: name, Dev: dev}

		if info, err := io.GPIOChipInfo(dev); err == nil {
			c.Label, c.Lines, c.Source = info.Label, info.Lines, "ioctl"
		} else {
			label, lines, serr := sysfsChipLabel(io, name)
			if serr != nil {
				warns = append(warns, Warning{
					Code: "gpio.label",
					Detail: fmt.Sprintf(
						"%s: label unreadable (ioctl: %v; sysfs: %v)", dev, err, serr),
				})
				chips = append(chips, c)
				continue
			}
			c.Label, c.Lines, c.Source = label, lines, "sysfs"
			warns = append(warns, Warning{
				Code:   "gpio.label.fallback",
				Detail: fmt.Sprintf("%s: ioctl failed (%v), used deprecated sysfs", dev, err),
			})
		}
		chips = append(chips, c)
	}

	markHeaderChip(chips, &warns)
	return chips, warns
}

// markHeaderChip flags the chip driving the 40-pin header, identified purely by
// label. If no label is recognised we warn rather than guess: a wrong guess
// produces a confidently-wrong --device flag that fails at open() time with a
// message the user cannot act on.
func markHeaderChip(chips []GPIOChip, warns *[]Warning) {
	for i := range chips {
		if _, ok := familyForLabel(chips[i].Label); ok {
			chips[i].Header = true
			return
		}
	}
	if len(chips) > 0 {
		labels := make([]string, 0, len(chips))
		for _, c := range chips {
			labels = append(labels, fmt.Sprintf("%s[%s]", c.Name, c.Label))
		}
		*warns = append(*warns, Warning{
			Code: "gpio.header.unknown",
			Detail: fmt.Sprintf(
				"no chip matched a known Raspberry Pi pinctrl label; found %s. "+
					"GPIO passthrough will be disabled. Please report this with "+
					"`ros2pi check --dump-facts`.", strings.Join(labels, " ")),
		})
	}
}

// devGPIOChipNames lists the real /dev/gpiochip* nodes in stable numeric order.
//
// Symlinks are skipped. Raspberry Pi OS ships /dev/gpiochip4 -> gpiochip0 on a
// Pi 4 as a compatibility shim for code written against the Pi 5's kernel-6.1
// numbering. It is the same device: an ioctl on it succeeds and reports the
// same label and line count, so treating it as a chip in its own right
// invents a phantom third chip. gpiodetect and /sys/bus/gpio/devices both list
// only the two real ones.
func devGPIOChipNames(io FileIO) ([]string, error) {
	entries, err := io.ReadDir("dev")
	if err != nil {
		return nil, fmt.Errorf("read /dev: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "gpiochip") {
			continue
		}
		if e.Type()&fs.ModeSymlink != 0 {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Slice(names, func(i, j int) bool {
		return chipIndex(names[i]) < chipIndex(names[j])
	})
	return names, nil
}

func chipIndex(name string) int {
	n, err := strconv.Atoi(strings.TrimPrefix(name, "gpiochip"))
	if err != nil {
		return 1 << 30 // unparseable names sort last, deterministically
	}
	return n
}

// sysfsChipLabel reads /sys/class/gpio/chipN/{label,ngpio}.
//
// Deliberately NOT /sys/class/gpio/gpiochipNNN/ -- those are indexed by GPIO
// base (512, 570 on this Pi 4), which is unstable across kernels and does not
// align with the gpiochipN device name. The chipN form does align.
func sysfsChipLabel(io FileIO, chipName string) (string, uint32, error) {
	idx := chipIndex(chipName)
	base := path.Join("sys/class/gpio", fmt.Sprintf("chip%d", idx))

	label, err := readTrimmed(io, path.Join(base, "label"))
	if err != nil {
		return "", 0, err
	}
	var lines uint32
	if s, err := readTrimmed(io, path.Join(base, "ngpio")); err == nil {
		if n, err := strconv.ParseUint(s, 10, 32); err == nil {
			lines = uint32(n)
		}
	}
	return label, lines, nil
}
