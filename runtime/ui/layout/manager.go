package layout

import (
	"github.com/jroimartin/gocui"
	"github.com/sirupsen/logrus"
)

type Manager struct {
	lastX, lastY int
	elements     map[Location][]Layout
}

func NewManager() *Manager {
	return &Manager{
		elements: make(map[Location][]Layout),
	}
}

func (lm *Manager) Add(element Layout, location Location) {
	if _, exists := lm.elements[location]; !exists {
		lm.elements[location] = make([]Layout, 0)
	}
	lm.elements[location] = append(lm.elements[location], element)
}

func (lm *Manager) layoutHeaders(g *gocui.Gui, area Area) (Area, error) {
	// layout headers top down
	if elements, exists := lm.elements[LocationHeader]; exists {
		for _, element := range elements {
			// a visible header cannot take up the whole screen, default to 1.
			// this eliminates the need to discover a default size based on all element requests
			height := 0
			if element.IsVisible() {
				requestedHeight := element.RequestedSize(area.maxY)
				if requestedHeight != nil {
					height = *requestedHeight
				} else {
					height = 1
				}
			}

			// layout the header within the allocated space
			err := element.Layout(g, area.minX, area.minY, area.maxX, area.minY+height)
			if err != nil {
				logrus.Errorf("failed to layout '%s' header: %+v", element.Name(), err)
				return area, err
			}

			// restrict the available screen real estate
			area.minY += height

		}
	}
	return area, nil
}

func (lm *Manager) planFooters(g *gocui.Gui, area Area) (Area, []int) {
	var footerHeights = make([]int, 0)
	// we need to layout the footers last, but account for them when drawing the columns. This block is for planning
	// out the real estate needed for the footers now (but not laying out yet)
	if elements, exists := lm.elements[LocationFooter]; exists {
		footerHeights = make([]int, len(elements))
		for idx := range footerHeights {
			footerHeights[idx] = 1
		}

		for idx, element := range elements {
			// a visible footer cannot take up the whole screen, default to 1.
			// this eliminates the need to discover a default size based on all element requests
			height := 0
			if element.IsVisible() {
				requestedHeight := element.RequestedSize(area.maxY)
				if requestedHeight != nil {
					height = *requestedHeight
				} else {
					height = 1
				}
			}
			footerHeights[idx] = height
		}
		// restrict the available screen real estate
		for _, height := range footerHeights {
			area.maxY -= height
		}
	}
	return area, footerHeights
}

func (lm *Manager) layoutColumns(g *gocui.Gui, area Area) (Area, error) {
	// layout columns left to right
	if elements, exists := lm.elements[LocationColumn]; exists {
		widths := make([]int, len(elements))
		for idx := range widths {
			widths[idx] = -1
		}
		variableColumns := len(elements)
		availableWidth := area.maxX

		// first pass: planout the column sizes based on the given requests
		for idx, element := range elements {
			if !element.IsVisible() {
				widths[idx] = 0
				variableColumns--
				continue
			}

			requestedWidth := element.RequestedSize(availableWidth)
			if requestedWidth != nil {
				widths[idx] = *requestedWidth
				variableColumns--
				availableWidth -= widths[idx]
			}
		}

		defaultWidth := int(availableWidth / variableColumns)

		// second pass: layout columns left to right (based off predetermined widths)
		for idx, element := range elements {
			// use the requested or default width
			width := widths[idx]
			if width == -1 {
				width = defaultWidth
			}

			// layout the column within the allocated space
			err := element.Layout(g, area.minX, area.minY, area.minX+width, area.maxY)
			if err != nil {
				logrus.Errorf("failed to layout '%s' column: %+v", element.Name(), err)
				return area, err
			}

			// move left to right, scratching off real estate as it is taken
			area.minX += width

		}
	}
	return area, nil
}

func (lm *Manager) layoutFooters(g *gocui.Gui, area Area, footerHeights []int) (Area, error) {
	// layout footers top down (which is why the list is reversed). Top down is needed due to border overlap.
	if elements, exists := lm.elements[LocationFooter]; exists {
		for idx := len(elements) - 1; idx >= 0; idx-- {
			element := elements[idx]
			height := footerHeights[idx]
			var topY, bottomY, bottomPadding int
			for oIdx := 0; oIdx <= idx; oIdx++ {
				bottomPadding += footerHeights[oIdx]
			}
			topY = area.maxX - bottomPadding - height
			// +1 for border
			bottomY = topY + height + 1

			// layout the footer within the allocated space
			// note: since the headers and rows are inclusive counting from -1 (to account for a border) we must
			// do the same vertically, thus a -1 is needed for a starting Y
			err := element.Layout(g, area.minX, topY, area.maxX, bottomY)
			if err != nil {
				logrus.Errorf("failed to layout '%s' footer: %+v", element.Name(), err)
				return area, err
			}
		}
	}
	return area, nil
}

func (lm *Manager) notifyLayoutChange() error {
	for _, elements := range lm.elements {
		for _, element := range elements {
			err := element.OnLayoutChange()
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// layout defines the definition of the window pane size and placement relations to one another. This
// is invoked at application start and whenever the screen dimensions change.
// A few things to note:
//  1. gocui has borders around all views (even if Frame=false). This means there are a lot of +1/-1 magic numbers
//     needed (but there are comments!).
//  2. since there are borders, in order for it to appear as if there aren't any spaces for borders, the views must
//     overlap. To prevent screen artifacts, all elements must be layedout from the top of the screen to the bottom.
func (lm *Manager) Layout(g *gocui.Gui) error {

	curMaxX, curMaxY := g.Size()
	area := Area{
		minX: -1,
		minY: -1,
		maxX: curMaxX,
		maxY: curMaxY,
	}

	var hasResized bool
	if curMaxX != lm.lastX || curMaxY != lm.lastY {
		hasResized = true
	}
	lm.lastX, lm.lastY = curMaxX, curMaxY

	// plan and layout all headers
	area, err := lm.layoutHeaders(g, area)
	if err != nil {
		return err
	}

	// plan all footers, don't layout until all columns have been layedout. This is necessary since we must layout from
	// top to bottom, but we need the real estate planned for the footers to determine the bottom of the columns.
	var footerArea = area
	area, footerHeights := lm.planFooters(g, area)

	// plan and layout the main columns
	area, err = lm.layoutColumns(g, area)
	if err != nil {
		return nil
	}

	// layout the footers according to the original available area and planned heights
	area, err = lm.layoutFooters(g, footerArea, footerHeights)
	if err != nil {
		return nil
	}

	// notify everyone of a layout change (allow to update and render)
	if hasResized {
		return lm.notifyLayoutChange()
	}

	return nil
}
